package installer

import (
	"fmt"
	"strings"

	"github.com/techiescamp/k8s-provisioner/internal/config"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

type Ollama struct {
	config *config.Config
	exec   executor.CommandExecutor
}

func NewOllama(cfg *config.Config, exec executor.CommandExecutor) *Ollama {
	return &Ollama{config: cfg, exec: exec}
}

// isCloudModel checks if the model is a cloud model (e.g., minimax-m2.5:cloud)
func (o *Ollama) isCloudModel() bool {
	model := o.config.KarporAI.Model
	return strings.HasSuffix(model, ":cloud")
}

// hasAPIKey checks if an Ollama API key is configured
func (o *Ollama) hasAPIKey() bool {
	return o.config.Ollama.APIKey != ""
}

func (o *Ollama) Install() error {
	fmt.Println("Installing Ollama...")

	model := o.config.KarporAI.Model
	isCloud := o.isCloudModel()

	if isCloud {
		fmt.Printf("Using cloud model: %s\n", model)
		if !o.hasAPIKey() {
			fmt.Println("WARNING: Cloud model requires API key. Get one at https://ollama.com/settings/keys")
			fmt.Println("         Set ollama.api_key in config.yaml")
		}
	} else {
		fmt.Printf("Using local model: %s\n", model)
	}

	// Label node01 for AI workloads (may fail if node01 hasn't joined yet)
	_, _ = o.exec.RunShell("kubectl label node node01 workload/ai=true --overwrite 2>/dev/null")

	// Create namespace
	fmt.Println("Creating Ollama namespace...")
	ns := `apiVersion: v1
kind: Namespace
metadata:
  name: ollama`
	if err := executor.WriteFile("/tmp/ollama-ns.yaml", ns); err != nil {
		return err
	}
	if _, err := o.exec.RunShell("kubectl apply -f /tmp/ollama-ns.yaml"); err != nil {
		return err
	}

	// Create API key secret if provided
	if o.hasAPIKey() {
		fmt.Println("Creating Ollama API key secret...")
		if err := o.createAPIKeySecret(); err != nil {
			return err
		}
	}

	// Create persistent storage for Ollama models (only needed for local models)
	if !isCloud {
		fmt.Println("Creating Ollama storage...")
		if err := o.createStorage(); err != nil {
			return err
		}
	}

	// Create deployment and service
	fmt.Println("Deploying Ollama...")
	manifest := o.buildDeploymentManifest(isCloud)

	if err := executor.WriteFile("/tmp/ollama-deploy.yaml", manifest); err != nil {
		return err
	}
	if _, err := o.exec.RunShell("kubectl apply -f /tmp/ollama-deploy.yaml"); err != nil {
		return err
	}

	// Create a Job to pull the model (only for local models)
	if !isCloud && model != "" {
		fmt.Printf("Creating model pull job for: %s...\n", model)
		if err := o.createModelPullJob(model); err != nil {
			fmt.Printf("Warning: Failed to create model pull job: %v\n", err)
		}
	} else if isCloud {
		fmt.Printf("Cloud model %s will be accessed via Ollama cloud API\n", model)
	}

	fmt.Println("Ollama installed successfully!")
	if isCloud {
		fmt.Println("Ollama is configured for cloud models at: http://ollama.ollama.svc:11434")
		fmt.Println("Cloud models: minimax-m2.5:cloud, qwen3-coder:480b-cloud, glm-4.7:cloud")
	} else {
		fmt.Println("Ollama is available at: http://ollama.ollama.svc:11434")
	}
	return nil
}

func (o *Ollama) buildDeploymentManifest(isCloud bool) string {
	// Base environment variables
	envVars := `        env:
        - name: OLLAMA_HOST
          value: "0.0.0.0:11434"`

	// Add API key environment variable if configured
	if o.hasAPIKey() {
		envVars += `
        - name: OLLAMA_API_KEY
          valueFrom:
            secretKeyRef:
              name: ollama-api-key
              key: api-key`
	}

	// Volume mounts and volumes (only for local models)
	volumeMounts := ""
	volumes := ""
	if !isCloud {
		volumeMounts = `
        volumeMounts:
        - name: ollama-data
          mountPath: /root/.ollama`
		volumes = `
      volumes:
      - name: ollama-data
        persistentVolumeClaim:
          claimName: ollama-data`
	}

	// Adjust resources for cloud models (less memory needed)
	resources := ""
	if isCloud {
		resources = `
        resources:
          requests:
            cpu: 100m
            memory: 256Mi
          limits:
            cpu: 500m
            memory: 512Mi`
	} else {
		resources = `
        resources:
          requests:
            cpu: 500m
            memory: 4Gi
          limits:
            cpu: 2
            memory: 6Gi`
	}

	return fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: ollama
  namespace: ollama
spec:
  replicas: 1
  strategy:
    type: Recreate
  selector:
    matchLabels:
      app: ollama
  template:
    metadata:
      labels:
        app: ollama
    spec:
      affinity:
        nodeAffinity:
          preferredDuringSchedulingIgnoredDuringExecution:
          - weight: 100
            preference:
              matchExpressions:
              - key: workload/ai
                operator: In
                values:
                - "true"
      containers:
      - name: ollama
        image: ollama/ollama:latest
        ports:
        - containerPort: 11434
%s%s
        readinessProbe:
          httpGet:
            path: /api/tags
            port: 11434
          initialDelaySeconds: 10
          periodSeconds: 5
          failureThreshold: 3
        livenessProbe:
          httpGet:
            path: /api/tags
            port: 11434
          initialDelaySeconds: 30
          periodSeconds: 10
          failureThreshold: 3%s%s
---
apiVersion: v1
kind: Service
metadata:
  name: ollama
  namespace: ollama
spec:
  selector:
    app: ollama
  ports:
  - port: 11434
    targetPort: 11434
  type: ClusterIP`, envVars, resources, volumeMounts, volumes)
}

func (o *Ollama) createAPIKeySecret() error {
	// Delete existing secret if exists
	_, _ = o.exec.RunShell("kubectl delete secret ollama-api-key -n ollama 2>/dev/null || true")

	// Create secret with API key
	cmd := fmt.Sprintf("kubectl create secret generic ollama-api-key -n ollama --from-literal=api-key=%s", o.config.Ollama.APIKey)
	_, err := o.exec.RunShell(cmd)
	if err != nil {
		return fmt.Errorf("failed to create API key secret: %w", err)
	}
	fmt.Println("Ollama API key secret created successfully")
	return nil
}

func (o *Ollama) createStorage() error {
	nfsServer := o.config.Storage.NFSServer
	if nfsServer == "" {
		nfsServer = "storage"
	}
	nfsPath := o.config.Storage.NFSPath
	if nfsPath == "" {
		nfsPath = "/exports/k8s-volumes"
	}

	// Create directory on NFS via local mount
	fmt.Println("Creating Ollama storage directory on NFS...")
	mkdirCmd := "mkdir -p /mnt/nfs-storage/ollama && chmod 777 /mnt/nfs-storage/ollama"
	if _, err := o.exec.RunShell(mkdirCmd); err != nil {
		fmt.Printf("Warning: Failed to create directory on NFS: %v\n", err)
	}

	// Create PV and PVC for Ollama data
	storage := fmt.Sprintf(`apiVersion: v1
kind: PersistentVolume
metadata:
  name: ollama-pv
spec:
  capacity:
    storage: 10Gi
  accessModes:
    - ReadWriteOnce
  persistentVolumeReclaimPolicy: Retain
  storageClassName: nfs-static
  claimRef:
    namespace: ollama
    name: ollama-data
  nfs:
    server: %s
    path: %s/ollama
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: ollama-data
  namespace: ollama
spec:
  accessModes:
    - ReadWriteOnce
  storageClassName: nfs-static
  resources:
    requests:
      storage: 10Gi`, nfsServer, nfsPath)

	if err := executor.WriteFile("/tmp/ollama-storage.yaml", storage); err != nil {
		return err
	}

	_, err := o.exec.RunShell("kubectl apply -f /tmp/ollama-storage.yaml")
	return err
}

func (o *Ollama) createModelPullJob(model string) error {
	// Create a Job that pulls the model using curl to Ollama API
	// This job will retry until Ollama is ready and the model is pulled
	job := fmt.Sprintf(`apiVersion: batch/v1
kind: Job
metadata:
  name: ollama-model-pull
  namespace: ollama
spec:
  backoffLimit: 30
  ttlSecondsAfterFinished: 300
  template:
    spec:
      restartPolicy: OnFailure
      containers:
      - name: pull-model
        image: curlimages/curl:latest
        command:
        - /bin/sh
        - -c
        - |
          echo "Waiting for Ollama service..."
          until curl -s http://ollama.ollama.svc:11434/api/tags > /dev/null 2>&1; do
            echo "Ollama not ready, waiting..."
            sleep 10
          done
          echo "Ollama is ready, pulling model %s..."
          curl -X POST http://ollama.ollama.svc:11434/api/pull -d '{"name": "%s"}' --max-time 600
          echo "Model pull completed!"`, model, model)

	if err := executor.WriteFile("/tmp/ollama-model-job.yaml", job); err != nil {
		return err
	}

	// Delete any existing job first
	_, _ = o.exec.RunShell("kubectl delete job ollama-model-pull -n ollama 2>/dev/null || true")

	_, err := o.exec.RunShell("kubectl apply -f /tmp/ollama-model-job.yaml")
	return err
}

