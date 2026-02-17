package installer

import (
	"fmt"
	"strings"
	"time"

	"github.com/techiescamp/k8s-provisioner/internal/config"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

type Karpor struct {
	config *config.Config
	exec   executor.CommandExecutor
}

func NewKarpor(cfg *config.Config, exec executor.CommandExecutor) *Karpor {
	return &Karpor{config: cfg, exec: exec}
}

func (k *Karpor) Install() error {
	fmt.Println("Installing Karpor (Kubernetes Explorer)...")

	// Detect architecture
	arch := k.detectArchitecture()
	fmt.Printf("Detected architecture: %s\n", arch)

	// Install Helm if not present
	fmt.Println("Checking Helm installation...")
	if err := k.installHelm(); err != nil {
		return err
	}

	// Add Helm repository
	fmt.Println("Adding Karpor Helm repository...")
	if _, err := k.exec.RunShell("helm repo add kusionstack https://kusionstack.github.io/charts"); err != nil {
		return err
	}
	if _, err := k.exec.RunShell("helm repo update"); err != nil {
		return err
	}

	// Create namespace with Helm labels to avoid conflicts
	fmt.Println("Creating Karpor namespace...")
	nsManifest := `apiVersion: v1
kind: Namespace
metadata:
  name: karpor
  labels:
    app.kubernetes.io/managed-by: Helm
  annotations:
    meta.helm.sh/release-name: karpor
    meta.helm.sh/release-namespace: karpor`
	if err := executor.WriteFile("/tmp/karpor-ns.yaml", nsManifest); err != nil {
		return err
	}
	if _, err := k.exec.RunShell("kubectl apply -f /tmp/karpor-ns.yaml"); err != nil {
		return err
	}

	// Create PVs for Karpor storage
	fmt.Println("Creating storage for Karpor...")
	if err := k.createStorage(); err != nil {
		return err
	}

	// Build Helm install/upgrade command (without --wait, we'll wait ourselves)
	fmt.Println("Installing Karpor via Helm...")
	helmArgs := fmt.Sprintf("helm upgrade --install karpor kusionstack/karpor -n karpor --version %s", k.config.Versions.Karpor)

	// Configure storage class for etcd and elasticsearch (static - uses pre-created PVs with claimRef)
	helmArgs += " --set etcd.persistence.storageClass=nfs-static"
	helmArgs += " --set elasticsearch.persistence.storageClass=nfs-static"

	// For amd64, use the chart's default version which works well

	// Reduce elasticsearch resources to fit in smaller nodes
	helmArgs += " --set elasticsearch.resources.requests.cpu=500m"
	helmArgs += " --set elasticsearch.resources.requests.memory=1Gi"
	helmArgs += " --set elasticsearch.resources.limits.cpu=1"
	helmArgs += " --set elasticsearch.resources.limits.memory=2Gi"

	// Reduce etcd resources
	helmArgs += " --set etcd.resources.requests.cpu=100m"
	helmArgs += " --set etcd.resources.requests.memory=256Mi"
	helmArgs += " --set etcd.resources.limits.cpu=500m"
	helmArgs += " --set etcd.resources.limits.memory=512Mi"

	// Add AI configuration if enabled
	if k.config.KarporAI.Enabled {
		// Disable AI proxy (required by chart)
		helmArgs += " --set server.ai.proxy.enabled=false"

		// For Ollama, we use "openai" backend since Ollama provides OpenAI-compatible API
		backend := k.config.KarporAI.Backend
		baseURL := k.config.KarporAI.BaseURL
		authToken := k.config.KarporAI.AuthToken
		model := k.config.KarporAI.Model

		if backend == "ollama" {
			backend = "openai"

			// Check if using cloud model (e.g., minimax-m2.5:cloud)
			isCloudModel := strings.HasSuffix(model, ":cloud")

			if isCloudModel {
				// Cloud models: Ollama proxies to ollama.com
				// The internal Ollama service handles authentication via OLLAMA_API_KEY
				if baseURL == "" {
					baseURL = "http://ollama.ollama.svc:11434"
				}
				// For cloud models, use API key from Ollama config if available
				if authToken == "" && k.config.Ollama.APIKey != "" {
					authToken = k.config.Ollama.APIKey
				}
				if authToken == "" {
					authToken = "not-needed" // Chart requires a value
				}
			} else {
				// Local models: use internal Ollama service directly
				if baseURL == "" {
					baseURL = "http://ollama.ollama.svc:11434"
				}
				if authToken == "" {
					authToken = "not-needed"
				}
			}

			// Ensure baseURL ends with /v1 for OpenAI compatibility
			if !strings.HasSuffix(baseURL, "/v1") {
				baseURL = strings.TrimSuffix(baseURL, "/") + "/v1"
			}
		}

		helmArgs += fmt.Sprintf(" --set server.ai.backend=%s", backend)

		if authToken != "" {
			helmArgs += fmt.Sprintf(" --set server.ai.authToken=%s", authToken)
		}
		if baseURL != "" {
			helmArgs += fmt.Sprintf(" --set server.ai.baseUrl=%s", baseURL)
		}
		if model != "" {
			helmArgs += fmt.Sprintf(" --set server.ai.model=%s", model)
		}
	}

	if err := k.exec.RunShellWithOutput(helmArgs); err != nil {
		return err
	}

	// Create kubeconfig ConfigMap for karpor-syncer to access the cluster
	fmt.Println("Creating kubeconfig for Karpor syncer...")
	if err := k.createKubeconfig(); err != nil {
		fmt.Printf("Warning: Failed to create kubeconfig: %v\n", err)
	}

	// Patch elasticsearch for ARM64 compatibility (disable SVE instructions)
	if arch == "arm64" {
		fmt.Println("Patching Elasticsearch for ARM64 compatibility...")
		if err := k.patchElasticsearchForARM64(); err != nil {
			fmt.Printf("Warning: Failed to patch Elasticsearch: %v\n", err)
		}
	}

	// Wait for components to be ready
	fmt.Println("Waiting for Karpor to be ready...")
	if err := k.waitForReady(DefaultReadyTimeout); err != nil {
		fmt.Printf("Warning: %v\n", err)
	}

	// Create Istio Gateway if Istio is enabled
	if k.config.Components.ServiceMesh == "istio" {
		fmt.Println("Creating Istio Gateway for Karpor...")
		if err := k.createIstioGateway(); err != nil {
			fmt.Printf("Warning: Failed to create Karpor gateway: %v\n", err)
		}
	}

	// Wait for Ollama model and restart karpor-server to enable AI
	if k.config.KarporAI.Enabled && k.config.KarporAI.Backend == "ollama" {
		fmt.Println("Waiting for Ollama model to be ready...")
		if err := k.waitForOllamaModel(); err != nil {
			fmt.Printf("Warning: %v\n", err)
		} else {
			fmt.Println("Restarting Karpor server to connect to AI...")
			_, _ = k.exec.RunShell("kubectl rollout restart deployment/karpor-server -n karpor")
			// Wait for karpor-server to be ready again
			time.Sleep(10 * time.Second)
			_, _ = k.exec.RunShell("kubectl wait --for=condition=Ready pods -l app.kubernetes.io/component=karpor-server -n karpor --timeout=120s")
			fmt.Println("Karpor AI should be functional now.")
		}
	}

	fmt.Println("Karpor installed successfully!")
	k.printAccessInfo()
	return nil
}

func (k *Karpor) detectArchitecture() string {
	out, err := k.exec.RunShell("uname -m")
	if err != nil {
		return "amd64" // default to amd64
	}

	// Normalize architecture names
	arch := strings.TrimSpace(out)
	switch arch {
	case "aarch64", "arm64":
		return "arm64"
	case "x86_64", "amd64":
		return "amd64"
	default:
		return "amd64"
	}
}

func (k *Karpor) createStorage() error {
	nfsServer := k.config.Storage.NFSServer
	if nfsServer == "" {
		nfsServer = "storage"
	}
	nfsPath := k.config.Storage.NFSPath
	if nfsPath == "" {
		nfsPath = "/exports/k8s-volumes"
	}

	// Create directories via local NFS mount (mounted at /mnt/nfs-storage on controlplane)
	fmt.Println("Creating Karpor storage directories on NFS...")
	mkdirCmd := "mkdir -p /mnt/nfs-storage/karpor-etcd /mnt/nfs-storage/karpor-elasticsearch && chmod 777 /mnt/nfs-storage/karpor-etcd /mnt/nfs-storage/karpor-elasticsearch"
	if _, err := k.exec.RunShell(mkdirCmd); err != nil {
		fmt.Printf("Warning: Failed to create directories on NFS: %v\n", err)
	}

	// Create PVs with claimRef to bind directly to the PVCs created by Helm
	// This ensures the PVs are reserved for Karpor's specific PVCs
	// Chart 0.7.6 requires 10Gi for etcd and elasticsearch
	storage := fmt.Sprintf(`apiVersion: v1
kind: PersistentVolume
metadata:
  name: karpor-etcd-pv
spec:
  capacity:
    storage: 10Gi
  accessModes:
    - ReadWriteOnce
  persistentVolumeReclaimPolicy: Retain
  storageClassName: nfs-static
  claimRef:
    namespace: karpor
    name: data-etcd-0
  nfs:
    server: %s
    path: %s/karpor-etcd
---
apiVersion: v1
kind: PersistentVolume
metadata:
  name: karpor-elasticsearch-pv
spec:
  capacity:
    storage: 10Gi
  accessModes:
    - ReadWriteOnce
  persistentVolumeReclaimPolicy: Retain
  storageClassName: nfs-static
  claimRef:
    namespace: karpor
    name: data-elasticsearch-0
  nfs:
    server: %s
    path: %s/karpor-elasticsearch`, nfsServer, nfsPath, nfsServer, nfsPath)

	if err := executor.WriteFile("/tmp/karpor-storage.yaml", storage); err != nil {
		return err
	}

	_, err := k.exec.RunShell("kubectl apply -f /tmp/karpor-storage.yaml")
	return err
}

func (k *Karpor) installHelm() error {
	// Check if helm is already installed
	if _, err := k.exec.RunShell("which helm"); err == nil {
		fmt.Println("Helm is already installed")
		return nil
	}

	fmt.Println("Installing Helm...")
	installCmd := "curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash"
	if err := k.exec.RunShellWithOutput(installCmd); err != nil {
		return fmt.Errorf("failed to install Helm: %w", err)
	}

	// Verify installation
	if _, err := k.exec.RunShell("helm version"); err != nil {
		return fmt.Errorf("helm installation verification failed: %w", err)
	}

	fmt.Println("Helm installed successfully")
	return nil
}

func (k *Karpor) waitForReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// Check if all pods are running using kubectl wait
		_, err := k.exec.RunShell("kubectl wait --for=condition=Ready pods --all -n karpor --timeout=10s 2>/dev/null")
		if err == nil {
			fmt.Println("Karpor is ready!")
			return nil
		}

		fmt.Println("Waiting for Karpor pods...")
		time.Sleep(LongPollInterval)
	}

	// Don't fail, just warn - pods might still be pulling images
	fmt.Println("Warning: Karpor pods may still be starting (timeout reached)")
	_ = k.exec.RunShellWithOutput("kubectl get pods -n karpor")
	return nil
}

func (k *Karpor) patchElasticsearchForARM64() error {
	// Patch to add ES_JAVA_OPTS with -XX:UseSVE=0 to disable SVE instructions that cause SIGILL on ARM64
	patch := `{"spec":{"template":{"spec":{"containers":[{"name":"elasticsearch","env":[{"name":"ES_JAVA_OPTS","value":"-XX:UseSVE=0"},{"name":"CLI_JAVA_OPTS","value":"-XX:UseSVE=0"}]}]}}}}`

	_, err := k.exec.RunShell(fmt.Sprintf("kubectl patch deployment elasticsearch -n karpor --type=strategic -p '%s'", patch))
	if err != nil {
		return err
	}

	// Restart the deployment to apply changes
	_, err = k.exec.RunShell("kubectl rollout restart deployment/elasticsearch -n karpor")
	return err
}

func (k *Karpor) createKubeconfig() error {
	// Delete existing ConfigMap if it exists (Helm creates an empty one)
	_, _ = k.exec.RunShell("kubectl delete configmap karpor-kubeconfig -n karpor 2>/dev/null || true")

	// Create ConfigMap with the actual kubeconfig
	_, err := k.exec.RunShell("kubectl create configmap karpor-kubeconfig -n karpor --from-file=config=/etc/kubernetes/admin.conf")
	if err != nil {
		return err
	}

	// Restart syncer to pick up the new kubeconfig
	_, _ = k.exec.RunShell("kubectl rollout restart deployment/karpor-syncer -n karpor 2>/dev/null || true")

	return nil
}

func (k *Karpor) waitForOllamaModel() error {
	model := k.config.KarporAI.Model
	if model == "" {
		model = "llama3.2:1b"
	}

	// Wait up to 10 minutes for the model to be available
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		// Check if Ollama pod is ready
		_, err := k.exec.RunShell("kubectl wait --for=condition=Ready pods -l app=ollama -n ollama --timeout=10s 2>/dev/null")
		if err != nil {
			fmt.Println("Waiting for Ollama pod...")
			time.Sleep(10 * time.Second)
			continue
		}

		// Check if model is available
		out, err := k.exec.RunShell("kubectl exec -n ollama deployment/ollama -- ollama list 2>/dev/null")
		if err == nil && strings.Contains(out, model) {
			fmt.Printf("Model %s is ready!\n", model)
			return nil
		}

		fmt.Printf("Waiting for model %s to be pulled...\n", model)
		time.Sleep(15 * time.Second)
	}

	return fmt.Errorf("timeout waiting for Ollama model %s", model)
}

func (k *Karpor) createIstioGateway() error {
	// Karpor uses self-signed TLS on port 7443
	// Istio connects with insecureSkipVerify since it's internal cluster traffic
	gateway := `apiVersion: networking.istio.io/v1
kind: Gateway
metadata:
  name: karpor-gateway
  namespace: karpor
spec:
  selector:
    istio: ingressgateway
  servers:
  - port:
      number: 80
      name: http
      protocol: HTTP
    hosts:
    - "karpor.local"
---
apiVersion: networking.istio.io/v1
kind: DestinationRule
metadata:
  name: karpor-server-tls
  namespace: karpor
spec:
  host: karpor-server
  trafficPolicy:
    tls:
      mode: SIMPLE
      insecureSkipVerify: true
---
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: karpor
  namespace: karpor
spec:
  hosts:
  - "karpor.local"
  gateways:
  - karpor-gateway
  http:
  - route:
    - destination:
        host: karpor-server
        port:
          number: 7443`

	if err := executor.WriteFile("/tmp/karpor-gateway.yaml", gateway); err != nil {
		return err
	}

	_, err := k.exec.RunShell("kubectl apply -f /tmp/karpor-gateway.yaml")
	return err
}

func (k *Karpor) printAccessInfo() {
	fmt.Println("\n========================================")
	fmt.Println("Karpor Access Information")
	fmt.Println("========================================")
	if k.config.Components.ServiceMesh == "istio" {
		fmt.Println("\nAccess via Istio Ingress:")
		fmt.Println("  1. Get Istio Ingress IP:")
		fmt.Println("     INGRESS_IP=$(kubectl get svc -n istio-system istio-ingressgateway -o jsonpath='{.status.loadBalancer.ingress[0].ip}')")
		fmt.Println("  2. Add to /etc/hosts:")
		fmt.Println("     echo \"$INGRESS_IP karpor.local\" | sudo tee -a /etc/hosts")
		fmt.Println("  3. Access: http://karpor.local")
	} else {
		fmt.Println("\nAccess via port-forward:")
		fmt.Println("  kubectl port-forward -n karpor svc/karpor-server 7443:7443")
		fmt.Println("  Then access: http://localhost:7443")
	}
	if k.config.KarporAI.Enabled {
		fmt.Println("\nAI Features: Enabled")
		fmt.Printf("  Backend: %s\n", k.config.KarporAI.Backend)
	} else {
		fmt.Println("\nAI Features: Disabled")
		fmt.Println("  To enable AI, configure karpor_ai in config.yaml")
	}
	fmt.Println("========================================")
}

