# k8s-provisioner

Kubernetes cluster provisioner written in Go for lab environments. Supports macOS, Linux and Windows.

## Stack

| Component | Version |
|-----------|---------|
| OS | Debian 12 |
| Container Runtime | CRI-O 1.32 |
| Kubernetes | 1.32 |
| CNI | Calico 3.28.0 |
| LoadBalancer | MetalLB 0.14.8 |
| Service Mesh | Istio 1.28.2 |
| Storage | NFS Server + Dynamic Provisioner |
| Metrics | Metrics Server |
| Monitoring | Prometheus + Grafana |
| Logging | Loki + Promtail |
| Kubernetes Explorer | Karpor 0.7.6 |
| AI Backend | Ollama (local/cloud) |

## Prerequisites

| Tool | Version | Installation |
|------|---------|--------------|
| VirtualBox | 7.0+ | [Download](https://www.virtualbox.org/wiki/Downloads) |
| Vagrant | 2.4+ | [Download](https://developer.hashicorp.com/vagrant/downloads) |
| kubectl | 1.32+ | [Install Guide](https://kubernetes.io/docs/tasks/tools/) |
| Go | 1.22+ | [Download](https://go.dev/dl/) (only for building) |

### macOS (Homebrew)

```bash
brew install --cask virtualbox vagrant
brew install kubectl go
```

### Ubuntu/Debian

```bash
# VirtualBox
sudo apt install virtualbox

# Vagrant
wget https://releases.hashicorp.com/vagrant/2.4.3/vagrant_2.4.3-1_amd64.deb
sudo dpkg -i vagrant_2.4.3-1_amd64.deb

# kubectl
curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl"
sudo install kubectl /usr/local/bin/

# Go
sudo snap install go --classic
```

### Windows

Use [Chocolatey](https://chocolatey.org/):

```powershell
choco install virtualbox vagrant kubernetes-cli golang
```

## Architecture

```
+------------------+     +------------------+     +------------------+
|     Storage      |     |   ControlPlane   |     |     Node01       |
|  192.168.56.20   |     |  192.168.56.10   |     |  192.168.56.11   |
|    NFS Server    |     |     Master       |     |     Worker       |
+------------------+     +------------------+     +------------------+
                                                  +------------------+
                                                  |     Node02       |
                                                  |  192.168.56.12   |
                                                  |     Worker       |
                                                  +------------------+
```

## Project Structure

```
k8s-provisioner/
├── cmd/                    # CLI commands
│   ├── root.go
│   ├── provision.go
│   ├── status.go
│   └── vbox.go            # VirtualBox management
├── internal/
│   ├── config/            # YAML config parser
│   ├── executor/          # Shell command executor
│   ├── installer/         # Calico, MetalLB, Istio
│   └── provisioner/       # Main provisioning logic
├── vagrant/               # Vagrant files
│   ├── Vagrantfile
│   └── settings.yaml
├── examples/              # Example manifests
│   ├── nfs-pv-pvc.yaml   # NFS PV/PVC example
│   └── podinfo-app.yaml  # Podinfo with Istio
├── build/                 # Compiled binaries
├── config.yaml            # Cluster configuration
├── go.mod
├── main.go
└── Makefile
```

## Pre-built Binaries

| File | Platform |
|------|----------|
| `k8s-provisioner-darwin-arm64` | macOS Apple Silicon |
| `k8s-provisioner-darwin-amd64` | macOS Intel |
| `k8s-provisioner-linux-arm64` | Linux ARM64 |
| `k8s-provisioner-linux-amd64` | Linux x64 |
| `k8s-provisioner-windows-amd64.exe` | Windows x64 |

## Quick Start

### 1. Build the binary

```bash
# Install dependencies
make deps

# Build for all platforms
make build-all

# Or build for specific platform
make build-linux-arm64
make build-darwin-arm64
make build-windows-amd64
```

### 2. Create the cluster

```bash
cd vagrant
vagrant up
```

### 3. Enable promiscuous mode (required for MetalLB)

```bash
# Using the CLI tool (macOS)
./build/k8s-provisioner-darwin-arm64 vbox promisc

# Windows
.\build\k8s-provisioner-windows-amd64.exe vbox promisc

# Linux
./build/k8s-provisioner-linux-amd64 vbox promisc
```

### 4. Access the cluster

```bash
# Copy kubeconfig
vagrant ssh controlplane -c 'sudo cat /etc/kubernetes/admin.conf' > ~/.kube/config-lab

# Adjust server IP
sed -i '' 's/127.0.0.1/192.168.56.10/' ~/.kube/config-lab

# Use the config
export KUBECONFIG=~/.kube/config-lab
kubectl get nodes
```

## CLI Commands

### Provisioning (runs inside VMs)

```bash
k8s-provisioner --help                    # Show help
k8s-provisioner version                   # Show versions
k8s-provisioner status                    # Show cluster status
k8s-provisioner provision common          # Install CRI-O, kubeadm
k8s-provisioner provision controlplane    # Initialize control plane
k8s-provisioner provision worker          # Join as worker
k8s-provisioner provision all             # Full provisioning (auto-detect role)
```

### VirtualBox Management (runs on host)

```bash
k8s-provisioner vbox promisc    # Enable promiscuous mode on all VMs
k8s-provisioner vbox status     # Show promiscuous mode status
k8s-provisioner vbox list       # List all VirtualBox VMs
```

**Why promiscuous mode?**

MetalLB uses Layer 2 mode (ARP) to announce LoadBalancer IPs. VirtualBox by default blocks ARP traffic between VMs and the host. Enabling promiscuous mode allows the host to receive ARP responses from MetalLB, making LoadBalancer IPs accessible from the host machine.

### User Management (runs on host)

Create and manage Kubernetes users with X.509 certificate-based authentication.

```bash
# Create user with cluster-wide view access
k8s-provisioner user create joao --cluster-role view

# Create user with edit access to a specific namespace
k8s-provisioner user create maria --namespace dev --cluster-role edit

# Create user in a group with admin access
k8s-provisioner user create pedro --group developers --cluster-role admin

# Create user with custom certificate expiration (default: 365 days)
k8s-provisioner user create ana --cluster-role view --expiration 30

# Create a developer role in a namespace
k8s-provisioner user create-role developer --namespace dev

# Assign user to a custom role
k8s-provisioner user create carlos --namespace dev --role developer

# List all users
k8s-provisioner user list

# Delete a user
k8s-provisioner user delete joao
```

**What gets created:**
- RSA private key (`~/.k8s-users/<username>/<username>.key`)
- X.509 certificate (`~/.k8s-users/<username>/<username>.crt`)
- Kubeconfig file (`~/.k8s-users/<username>/<username>.kubeconfig`)
- RBAC bindings (ClusterRoleBinding or RoleBinding)

**Using the generated kubeconfig:**

```bash
# Option 1: Direct use
kubectl --kubeconfig=~/.k8s-users/joao/joao.kubeconfig get pods

# Option 2: Export KUBECONFIG
export KUBECONFIG=~/.k8s-users/joao/joao.kubeconfig
kubectl get pods

# Option 3: Merge with existing config
KUBECONFIG=~/.kube/config:~/.k8s-users/joao/joao.kubeconfig kubectl config view --flatten > ~/.kube/config-merged
mv ~/.kube/config-merged ~/.kube/config
kubectl config use-context joao@k8s-lab
```

**Built-in ClusterRoles:**

| ClusterRole | Permissions |
|-------------|-------------|
| `view` | Read-only access to most resources |
| `edit` | Read/write access (no RBAC) |
| `admin` | Full access within namespace |
| `cluster-admin` | Full cluster access |

## Configuration

### config.yaml

```yaml
cluster:
  name: "k8s-lab"
  pod_cidr: "10.244.0.0/16"
  service_cidr: "10.96.0.0/12"

versions:
  kubernetes: "1.32"
  crio: "v1.32"
  calico: "3.28.0"
  metallb: "0.14.8"
  istio: "1.28.2"

network:
  interface: "eth1"
  controlplane_ip: "192.168.56.10"
  metallb_range: "192.168.56.200-192.168.56.250"

storage:
  nfs_server: "storage"       # Uses hostname from /etc/hosts
  nfs_path: "/exports/k8s-volumes"
  default_dynamic: true       # nfs-dynamic as default StorageClass

# Node definitions - IPs should match vagrant/settings.yaml
nodes:
  - name: "storage"
    role: "storage"
  - name: "controlplane"
    role: "controlplane"
  - name: "node01"
    role: "worker"
  - name: "node02"
    role: "worker"

components:
  cni: "calico"
  load_balancer: "metallb"
  service_mesh: "istio"
  monitoring: "prometheus-stack"  # Options: prometheus-stack, none
  logging: "loki"                 # Options: loki, none
  karpor: "enabled"               # Options: enabled, none

# Karpor AI configuration (optional)
karpor_ai:
  enabled: true
  backend: "ollama"           # Options: openai, azureopenai, huggingface, ollama
  model: "llama3.2:3b"        # Local model (or minimax-m2.5:cloud for cloud)

# Ollama cloud API key (only for :cloud models)
ollama:
  api_key: ""                 # Get from https://ollama.com/settings/keys
```

### vagrant/settings.yaml

```yaml
box_name: "bento/debian-12"
vm:
# Storage VM (NFS Server) - must be created first
- name: "storage"
  ip: "192.168.56.20"
  memory: "1024"
  cpus: "1"
  role: "storage"
# Kubernetes VMs
- name: "controlplane"
  ip: "192.168.56.10"
  memory: "6144"    # Extra for monitoring stack
  cpus: "4"
  role: "controlplane"
- name: "node01"
  ip: "192.168.56.11"
  memory: "8192"    # Extra for AI workloads (Ollama)
  cpus: "2"
  role: "worker"
- name: "node02"
  ip: "192.168.56.12"
  memory: "4096"
  cpus: "2"
  role: "worker"
```

## NFS Storage

The cluster includes a dedicated NFS server with dynamic and static provisioning support.

### StorageClasses

| StorageClass | Provisioning | Use Case |
|--------------|--------------|----------|
| `nfs-dynamic` | Automatic | PVCs auto-create PVs (recommended) |
| `nfs-static` | Manual | Pre-created PVs with specific paths |

### Dynamic Provisioning (Recommended)

Just create a PVC - the PV is created automatically:

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: my-data
spec:
  storageClassName: nfs-dynamic
  accessModes:
    - ReadWriteMany
  resources:
    requests:
      storage: 1Gi
```

```bash
kubectl apply -f my-pvc.yaml
kubectl get pvc  # PV created automatically!
```

### Static Provisioning

For specific NFS paths, create PV first:

```yaml
apiVersion: v1
kind: PersistentVolume
metadata:
  name: my-pv
spec:
  storageClassName: nfs-static
  capacity:
    storage: 5Gi
  accessModes:
    - ReadWriteMany
  nfs:
    server: 192.168.56.20
    path: /exports/k8s-volumes/my-data
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: my-pvc
spec:
  storageClassName: nfs-static
  accessModes:
    - ReadWriteMany
  resources:
    requests:
      storage: 5Gi
```

### Example Application with Storage

```bash
# Deploy example with dynamic and static storage
kubectl apply -f examples/nfs-pv-pvc.yaml

# Check resources
kubectl get pv
kubectl get pvc -A
kubectl get pods -n dynamic-demo
kubectl get pods -n static-demo
```

## Metrics Server

The cluster includes Metrics Server for resource monitoring.

### Usage

```bash
# View node resources
kubectl top nodes

# View pod resources
kubectl top pods

# View pods in all namespaces
kubectl top pods -A
```

### HPA (Horizontal Pod Autoscaler)

Metrics Server enables HPA for automatic scaling:

```bash
# Create HPA for a deployment
kubectl autoscale deployment my-app --cpu-percent=50 --min=1 --max=10

# Check HPA status
kubectl get hpa
```

## Monitoring Stack

The cluster includes a full monitoring stack with Prometheus and Grafana.

### Components

| Component | Description |
|-----------|-------------|
| Prometheus Operator | Manages Prometheus instances |
| Prometheus | Metrics collection and storage |
| Grafana | Visualization and dashboards |
| Node Exporter | Host metrics (CPU, memory, disk) |
| kube-state-metrics | Kubernetes object metrics |

### Accessing Grafana

Grafana is exposed via Istio Ingress Gateway:

```bash
# Get Istio Ingress IP
INGRESS_IP=$(kubectl get svc -n istio-system istio-ingressgateway -o jsonpath='{.status.loadBalancer.ingress[0].ip}')

# Add to /etc/hosts
echo "$INGRESS_IP grafana.local" | sudo tee -a /etc/hosts

# Access
open http://grafana.local
```

**Credentials:**
- Username: `admin`
- Password: `admin123`

### Accessing Prometheus

```bash
kubectl port-forward -n monitoring svc/prometheus 9090:9090
# Access: http://localhost:9090
```

### Recommended Dashboards

Import dashboards from grafana.com: **Dashboards** → **Import** → Enter ID → **Load**

#### Kubernetes Dashboards

| ID | Dashboard | Description |
|----|-----------|-------------|
| `15760` | Kubernetes / Views / Global | Cluster overview |
| `15757` | Kubernetes / Views / Namespaces | Per namespace metrics |
| `15759` | Kubernetes / Views / Pods | Pod details |
| `10000` | Kubernetes Cluster | Complete cluster view |
| `12740` | Kubernetes Monitoring | General monitoring |

#### Node Dashboards

| ID | Dashboard | Description |
|----|-----------|-------------|
| `1860` | Node Exporter Full | Detailed host metrics |

#### Java/JVM Dashboards

| ID | Dashboard | Description |
|----|-----------|-------------|
| `4701` | JVM Micrometer | Spring Boot with Micrometer |
| `8563` | JVM Dashboard | JMX Exporter metrics |
| `11955` | JVM Metrics | Heap, GC, Threads |
| `14430` | JVM Overview | Complete JVM view |

> **Note:** Java apps need to expose metrics via [Micrometer](https://micrometer.io/) or [JMX Exporter](https://github.com/prometheus/jmx_exporter)

#### Go Dashboards

| ID | Dashboard | Description |
|----|-----------|-------------|
| `10826` | Go Processes | Go runtime metrics |
| `6671` | Go Metrics | Goroutines, GC, Memory |
| `14061` | Go Runtime | Detailed runtime |

> **Note:** Go apps need to expose metrics via [prometheus/client_golang](https://github.com/prometheus/client_golang)

## Logging Stack (Loki)

The cluster includes Loki for log aggregation.

### Components

| Component | Description |
|-----------|-------------|
| Loki | Log aggregation and storage |
| Promtail | Log collector (DaemonSet on all nodes) |

### Accessing Logs

Logs are accessed via Grafana:

1. Open **http://grafana.local**
2. Go to **Explore** (left sidebar)
3. Select **Loki** as datasource

### LogQL Query Examples

```logql
# All logs from a namespace
{namespace="default"}

# Logs from kube-system
{namespace="kube-system"}

# Logs from specific pods
{pod=~"nginx.*"}

# Filter by container
{container="app"}

# Search for errors
{namespace="default"} |= "error"

# Search for errors (case insensitive)
{namespace="default"} |~ "(?i)error"

# Exclude patterns
{namespace="default"} != "health"

# Multiple filters
{namespace="default", container="app"} |= "error" != "timeout"

# Parse JSON logs
{namespace="default"} | json | level="error"

# Count errors per pod
sum by (pod) (count_over_time({namespace="default"} |= "error" [5m]))
```

### Recommended Log Dashboards

| ID | Dashboard | Description |
|----|-----------|-------------|
| `13639` | Loki Dashboard | General log overview |
| `12611` | Loki & Promtail | Logs with Promtail stats |
| `15141` | Loki Logs | Simple log viewer |

## Karpor (Kubernetes Explorer)

Karpor is a Kubernetes Explorer that provides intelligent search and AI-powered analysis.

> **Note:** Karpor requires extra resources (~1.5 CPU, ~2GB RAM). To disable it, set in `config.yaml`:
> ```yaml
> components:
>   karpor: "none"  # Options: enabled, none
> ```

### Features

- **Resource Search**: Find resources across the cluster with powerful queries
- **AI Analysis**: Natural language insights about your resources (powered by Ollama)
- **Dependency View**: Visualize relationships between resources

### Accessing Karpor

```bash
# Get Istio Ingress IP
INGRESS_IP=$(kubectl get svc -n istio-system istio-ingressgateway -o jsonpath='{.status.loadBalancer.ingress[0].ip}')

# Add to /etc/hosts
echo "$INGRESS_IP karpor.local" | sudo tee -a /etc/hosts

# Access
open http://karpor.local
```

### Search Examples

```
kind:Deployment                    # All deployments
namespace:monitoring kind:Pod      # Pods in monitoring namespace
label:app=nginx                    # Resources with label app=nginx
name:*api*                         # Resources with "api" in name
```

### Check Karpor Status

```bash
kubectl get pods -n karpor
kubectl logs -n karpor -l app.kubernetes.io/component=karpor-server
```

## Ollama (AI Backend)

Ollama provides AI capabilities for Karpor, supporting both local and cloud models.

> **Note:** Ollama is only installed when Karpor AI is enabled. To disable:
> ```yaml
> karpor_ai:
>   enabled: false  # Disables Ollama installation
> ```

### Local Models (Default)

Local models run inside the cluster on node01:

```yaml
# config.yaml
karpor_ai:
  enabled: true
  backend: "ollama"
  model: "llama3.2:3b"   # Runs locally (~4GB RAM)

ollama:
  api_key: ""            # Not needed for local models
```

**Available local models:**

| Model | RAM | Quality | Speed |
|-------|-----|---------|-------|
| `llama3.2:1b` | ~2GB | Basic | Very fast |
| `llama3.2:3b` | ~4GB | Good | Fast |
| `qwen2.5-coder:7b` | ~8GB | Excellent for code | Moderate |
| `llama3.1:8b` | ~10GB | Excellent | Slower |

### Cloud Models (Optional)

Cloud models offer better performance without GPU requirements:

1. Create account at https://ollama.com/signup
2. Generate API key at https://ollama.com/settings/keys
3. Configure:

```yaml
# config.yaml
karpor_ai:
  enabled: true
  backend: "ollama"
  model: "minimax-m2.5:cloud"   # Cloud model

ollama:
  api_key: "olka_your_key_here"  # Required for cloud models
```

**Available cloud models:**

| Model | Description |
|-------|-------------|
| `minimax-m2.5:cloud` | Top performer, comparable to Claude Opus |
| `qwen3-coder:480b-cloud` | Excellent for code analysis |
| `glm-4.7:cloud` | Good general purpose |

### Check Ollama Status

```bash
# Check pods
kubectl get pods -n ollama

# Check if model is loaded
kubectl exec -n ollama deployment/ollama -- ollama list

# Check logs
kubectl logs -n ollama deployment/ollama

# Test AI endpoint
kubectl exec -n ollama deployment/ollama -- curl -s localhost:11434/api/tags
```

### Switching Models

To change the model after installation:

```bash
# Pull new model
kubectl exec -n ollama deployment/ollama -- ollama pull llama3.1:8b

# Restart Karpor to use new model
kubectl rollout restart deployment/karpor-server -n karpor
```

## Test Applications

### Podinfo App

```bash
# Deploy
kubectl apply -f examples/podinfo-app.yaml

# Add to /etc/hosts
INGRESS_IP=$(kubectl get svc -n istio-system istio-ingressgateway \
  -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
echo "$INGRESS_IP podinfo.local" | sudo tee -a /etc/hosts

# Test
curl http://podinfo.local
```

### Httpbin (Istio sample)

```bash
# Create namespace with Istio injection
kubectl create namespace demo
kubectl label namespace demo istio-injection=enabled

# Deploy httpbin
kubectl apply -n demo -f https://raw.githubusercontent.com/istio/istio/release-1.28/samples/httpbin/httpbin.yaml

# Create Gateway and VirtualService
kubectl apply -f - <<EOF
apiVersion: networking.istio.io/v1
kind: Gateway
metadata:
  name: httpbin-gateway
  namespace: demo
spec:
  selector:
    istio: ingressgateway
  servers:
  - port:
      number: 80
      name: http
      protocol: HTTP
    hosts:
    - "httpbin.local"
---
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: httpbin
  namespace: demo
spec:
  hosts:
  - "httpbin.local"
  - "httpbin.demo.svc.cluster.local"
  gateways:
  - httpbin-gateway
  - mesh
  http:
  - route:
    - destination:
        host: httpbin
        port:
          number: 8000
EOF

# Add to /etc/hosts and test
echo "$INGRESS_IP httpbin.local" | sudo tee -a /etc/hosts
curl http://httpbin.local/headers
```

## Kubectl Aliases

The following aliases are pre-configured in the VMs:

```bash
alias k=kubectl
alias kgp='kubectl get pods'
alias kgs='kubectl get svc'
alias kgn='kubectl get nodes'
alias kga='kubectl get all'
alias kgaa='kubectl get all -A'
alias kd='kubectl describe'
alias kl='kubectl logs'
alias kx='kubectl exec -it'
alias ka='kubectl apply -f'
alias kdel='kubectl delete -f'
alias kn='kubectl config set-context --current --namespace'

# Dry-run helper
export do='--dry-run=client -o yaml'

# Example: Create a pod YAML
kubectl run nginx --image=nginx $do > nginx.yaml
```

## Troubleshooting

### MetalLB IP not reachable from host

```bash
# Use the CLI tool
./build/k8s-provisioner-darwin-arm64 vbox promisc

# Or manually
VBoxManage controlvm "Storage" nicpromisc2 allow-all
VBoxManage controlvm "Master" nicpromisc2 allow-all
VBoxManage controlvm "Node01" nicpromisc2 allow-all
VBoxManage controlvm "Node02" nicpromisc2 allow-all
```

### Pods stuck in Pending (control-plane taint)

```bash
kubectl taint nodes controlplane node-role.kubernetes.io/control-plane:NoSchedule-
```

### NFS mount issues

```bash
# Check NFS server is running
vagrant ssh storage -c 'systemctl status nfs-kernel-server'

# Check exports
vagrant ssh storage -c 'exportfs -v'

# Test mount from node
vagrant ssh node01 -c 'showmount -e 192.168.56.20'
```

### Check VirtualBox VMs

```bash
# List all VMs
./build/k8s-provisioner-darwin-arm64 vbox list

# Check promiscuous mode status
./build/k8s-provisioner-darwin-arm64 vbox status
```

### Clean install (reset everything)

```bash
cd vagrant

# Run cleanup script
./clean.sh

# Create cluster again
vagrant up
```

The `clean.sh` script will:
- Destroy all Vagrant VMs
- Remove local `.vagrant` metadata
- Clean Vagrant temporary cache
- Remove orphan VMs from VirtualBox
- Optionally remove the box to download fresh

## Resource Requirements

| VM | Memory | CPUs | Disk | Purpose |
|----|--------|------|------|---------|
| Storage | 1 GB | 1 | 10 GB | NFS Server |
| ControlPlane | 6 GB | 4 | 20 GB | K8s Master + Monitoring |
| Node01 | 8 GB | 2 | 20 GB | Worker + AI Workloads |
| Node02 | 4 GB | 2 | 20 GB | Worker |
| **Total** | **19 GB** | **9** | **70 GB** | |

> **Note:** Node01 has extra memory for Ollama AI workloads (model loading requires ~4GB for llama3.2:3b)

## GitFlow & CI/CD

The project uses [GitFlow](https://nvie.com/posts/a-successful-git-branching-model/) branching model with GitHub Actions for CI/CD.

### Branches

| Branch | Purpose |
|--------|---------|
| `main` | Production-ready code |
| `develop` | Development integration |
| `feature/*` | New features |
| `hotfix/*` | Production fixes |
| `release/*` | Release preparation |

### Workflow

```
feature/my-feature
        │
        ▼ (PR)
    develop
        │
        ▼ (PR)
      main ──────► auto tag + release
        │
        ▼
   hotfix/fix (merge back to develop)
```

**Flow:**
1. `feature/*` → PR to `develop`
2. `develop` → PR to `main`
3. Merge to `main` → **automatic tag and release** (reads `VERSION` file)

### Creating a Feature

```bash
# Create feature branch from develop
git checkout develop
git pull origin develop
git checkout -b feature/my-feature

# Work on your feature...
git add .
git commit -m "Add my feature"

# Push and create PR to develop
git push origin feature/my-feature
```

### Creating a Release (Automated)

Releases are created automatically when PRs are merged to `main`:

1. **Create feature branch** and update `VERSION` file:
   ```bash
   git checkout develop
   git checkout -b feature/my-feature

   # Make changes...
   echo "1.3.0" > VERSION
   git add .
   git commit -m "Add feature and bump version to 1.3.0"
   git push origin feature/my-feature
   ```

2. **Create PR** feature → develop, then merge

3. **Create PR** develop → main, then merge

4. **Automatic release** - GitHub Actions will:
   - Read version from `VERSION` file
   - Create tag `v1.3.0`
   - Build binaries for all platforms
   - Create GitHub Release with artifacts

### Creating a Hotfix

```bash
# Create hotfix from main
git checkout main
git pull origin main
git checkout -b hotfix/critical-fix

# Fix the issue...
git add .
git commit -m "Fix critical bug"

# Merge to main and tag
git checkout main
git merge hotfix/critical-fix
git tag -a v1.0.1 -m "Hotfix v1.0.1"
git push origin main --tags

# Merge back to develop
git checkout develop
git merge hotfix/critical-fix
git push origin develop
```

### Automatic Releases

When code is merged to `main`, GitHub Actions automatically:

1. Reads version from `VERSION` file
2. Creates git tag if it doesn't exist
3. Runs tests
4. Builds binaries for all platforms
5. Generates checksums
6. Creates a GitHub Release with all artifacts

**No manual tagging required!** Just update the `VERSION` file in your feature branch.

### Version Format

| Format | Description |
|--------|-------------|
| `v1.0.0` | Stable release |
| `v1.0.0-rc.1` | Release candidate |
| `v1.0.0-beta.1` | Beta release |
| `v1.0.0-alpha.1` | Alpha release |

### Check Version

```bash
# From Makefile
make version

# From binary
./build/k8s-provisioner-darwin-arm64 version
```

Example output:
```
k8s-provisioner v1.0.0
  Git Commit: 10ab11f
  Build Date: 2026-01-22T02:28:06Z
  Go Version: go1.22
  Platform:   darwin/arm64
```

## License

MIT