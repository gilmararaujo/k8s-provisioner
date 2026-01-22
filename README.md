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
| Storage | NFS Server |

## Architecture

```
+------------------+     +------------------+     +------------------+
|     Storage      |     |   ControlPlane   |     |     Node01       |
|  192.168.201.20  |     |  192.168.201.10  |     |  192.168.201.11  |
|    NFS Server    |     |     Master       |     |     Worker       |
+------------------+     +------------------+     +------------------+
                                                  +------------------+
                                                  |     Node02       |
                                                  |  192.168.201.12  |
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
sed -i '' 's/127.0.0.1/192.168.201.10/' ~/.kube/config-lab

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
  controlplane_ip: "192.168.201.10"
  metallb_range: "192.168.201.200-192.168.201.250"

storage:
  nfs_server: "192.168.201.20"
  nfs_path: "/exports/k8s-volumes"

nodes:
  - name: "storage"
    ip: "192.168.201.20"
    role: "storage"
  - name: "controlplane"
    ip: "192.168.201.10"
    role: "controlplane"
  - name: "node01"
    ip: "192.168.201.11"
    role: "worker"
  - name: "node02"
    ip: "192.168.201.12"
    role: "worker"
```

### vagrant/settings.yaml

```yaml
box_name: "bento/debian-12"
vm:
- name: "storage"
  ip: "192.168.201.20"
  memory: "2048"
  cpus: "1"
  role: "storage"
- name: "controlplane"
  ip: "192.168.201.10"
  memory: "4096"
  cpus: "2"
  role: "controlplane"
- name: "node01"
  ip: "192.168.201.11"
  memory: "4096"
  cpus: "2"
  role: "worker"
- name: "node02"
  ip: "192.168.201.12"
  memory: "4096"
  cpus: "2"
  role: "worker"
```

## NFS Storage

The cluster includes a dedicated NFS server for persistent volumes.

### NFS Exports

```
/exports/k8s-volumes/pv01  (1Gi)
/exports/k8s-volumes/pv02  (2Gi)
/exports/k8s-volumes/pv03  (5Gi)
```

### Using PV/PVC

```bash
# Deploy the example
kubectl apply -f examples/nfs-pv-pvc.yaml

# Check resources
kubectl get pv
kubectl get pvc -n nfs-demo
kubectl get pods -n nfs-demo

# Access via Istio Ingress
INGRESS_IP=$(kubectl get svc -n istio-system istio-ingressgateway \
  -o jsonpath='{.status.loadBalancer.ingress[0].ip}')

# Add to /etc/hosts
echo "$INGRESS_IP nginx-pvc.local" | sudo tee -a /etc/hosts

# Test
curl http://nginx-pvc.local
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
vagrant ssh node01 -c 'showmount -e 192.168.201.20'
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

| VM | Memory | CPUs | Disk |
|----|--------|------|------|
| Storage | 2 GB | 1 | 10 GB |
| ControlPlane | 4 GB | 2 | 20 GB |
| Node01 | 4 GB | 2 | 20 GB |
| Node02 | 4 GB | 2 | 20 GB |
| **Total** | **14 GB** | **7** | **70 GB** |

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
feature/new-feature
        │
        ▼
    develop ──────► release/v1.0.0 ──────► main
        ▲                                    │
        │                                    ▼
        └─────────────── hotfix/fix ◄────── tag v1.0.0
```

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

### Creating a Release

```bash
# Create release branch from develop
git checkout develop
git pull origin develop
git checkout -b release/v1.0.0

# Update version, fix bugs...
git add .
git commit -m "Prepare release v1.0.0"

# Merge to main
git checkout main
git merge release/v1.0.0

# Create tag (triggers automatic release)
git tag -a v1.0.0 -m "Release v1.0.0"
git push origin main --tags

# Merge back to develop
git checkout develop
git merge release/v1.0.0
git push origin develop
```

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

When you push a tag starting with `v`, GitHub Actions automatically:

1. Runs tests
2. Builds binaries for all platforms
3. Generates checksums
4. Creates a GitHub Release with all artifacts

```bash
# Just create and push the tag
git tag -a v1.0.0 -m "Release v1.0.0"
git push origin v1.0.0

# GitHub Actions does the rest!
```

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