# k8s-provisioner

Kubernetes cluster provisioner written in Go for lab environments. Supports macOS, Linux and Windows.

## Stack

| Component | Version |
|-----------|---------|
| OS | Debian 13 "Trixie" |
| Container Runtime | CRI-O 1.34 |
| Kubernetes | 1.34 |
| CNI | Calico 3.31.5 |
| LoadBalancer | MetalLB 0.15.3 |
| Service Mesh | Istio 1.29.2 + Kiali 2.24.0 |
| Storage | NFS Server + Dynamic Provisioner |
| Secrets Management | HashiCorp Vault |
| Metrics | Metrics Server + Prometheus Operator 0.90.1 |
| Monitoring | Prometheus + Grafana 13.0.1 + node-exporter 1.11.1 |
| Logging | Loki 3.7.1 + Grafana Alloy 1.15.1 |
| Tracing | Grafana Tempo 2.10.4 + OpenTelemetry Collector 0.150.0 |
| Identity Provider | Keycloak 26.2 |
| Kubernetes Explorer | Karpor 0.7.6 (disabled by default) |
| AI Backend | Ollama (local/cloud, disabled by default) |

## Prerequisites

| Tool | Version | Installation |
|------|---------|--------------|
| VirtualBox | 7.0+ | [Download](https://www.virtualbox.org/wiki/Downloads) |
| Vagrant | 2.4+ | [Download](https://developer.hashicorp.com/vagrant/downloads) |
| kubectl | 1.34+ | [Install Guide](https://kubernetes.io/docs/tasks/tools/) |
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
  ┌─────────────────────────────────────────────────────────────────────┐
  │                         192.168.56.0/24                             │
  │                                                                     │
  │  ┌──────────────────┐                  ┌────────────────────────┐   │
  │  │    Storage       │── NFS mount ────►│    ControlPlane        │   │
  │  │  192.168.56.20   │◄─ Vault auth ────│    192.168.56.10       │   │
  │  │──────────────────│                  │────────────────────────│   │
  │  │  NFS Server      │                  │  kubeadm · Calico      │   │
  │  │  Vault :8200     │                  │  MetalLB · Istio       │   │
  │  └────────┬─────────┘                  │  Prometheus · Loki     │   │
  │           │                            └──────────┬─────────────┘   │
  │           │ NFS mount                             │                 │
  │           │ Vault auth                       kubeadm join           │
  │           │                             ┌─────────┴────────┐        │
  │           │                        ┌────┴──────┐    ┌───────┴─────┐  │
  │           ├───────────────────────►│  Node01   │    │   Node02   │  │
  │           └───────────────────────►│ .56.11    │    │   .56.12   │  │
  │                                    │  Worker   │    │   Worker   │  │
  │                                    │ Ollama    │    │            │  │
  │                                    │ (opcional)│    │            │  │
  │                                    └───────────┘    └────────────┘  │
  └─────────────────────────────────────────────────────────────────────┘
```

O Vault roda no storage node **fora do cluster Kubernetes**. Isso garante que os secrets continuam acessíveis mesmo que o cluster tenha problemas.

## Project Structure

```
k8s-provisioner/
├── cmd/                    # CLI commands
│   ├── root.go
│   ├── provision.go
│   ├── status.go
│   ├── user.go            # User management
│   ├── vault.go           # Vault commands
│   └── vbox.go            # VirtualBox management
├── internal/
│   ├── config/            # YAML config parser
│   ├── executor/          # Shell command executor
│   ├── installer/         # Component installers
│   │   ├── calico.go
│   │   ├── istio.go
│   │   ├── karpor.go
│   │   ├── loki.go
│   │   ├── metallb.go
│   │   ├── metrics.go
│   │   ├── monitoring.go
│   │   ├── nfs_provisioner.go
│   │   ├── ollama.go
│   │   └── vault.go       # Vault init, unseal, k8s auth, secrets
│   └── provisioner/       # Main provisioning logic
├── vagrant/               # Vagrant files
│   ├── Vagrantfile
│   └── settings.yaml
├── examples/              # Example manifests
│   ├── nfs-pv-pvc.yaml
│   ├── podinfo-app.yaml
│   ├── vault-secret-app.yaml  # App de exemplo usando Vault
│   ├── vault-usage.md         # Guia do Vault
│   └── monitoring-access.md
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

### 5. Configure /etc/hosts (on your Mac/Linux host)

Add all service hostnames to your local `/etc/hosts` to access them by name:

```bash
# Get Istio Ingress IP (MetalLB LoadBalancer)
INGRESS_IP=$(kubectl get svc -n istio-system istio-ingressgateway \
  -o jsonpath='{.status.loadBalancer.ingress[0].ip}')

# Kubernetes services (via Istio Ingress Gateway)
echo "$INGRESS_IP grafana.local prometheus.local alertmanager.local kiali.local karpor.local keycloak.local" \
  | sudo tee -a /etc/hosts

# Storage node services (direct IP — fixed)
echo "192.168.56.20 vault.local" | sudo tee -a /etc/hosts
```

| Hostname | Service | URL | Credentials |
|----------|---------|-----|-------------|
| `grafana.local` | Grafana | https://grafana.local | `admin` / Vault |
| `prometheus.local` | Prometheus | https://prometheus.local | — |
| `alertmanager.local` | Alertmanager | https://alertmanager.local | — |
| `kiali.local` | Kiali (Istio) | https://kiali.local | — |
| `karpor.local` | Karpor Explorer | https://karpor.local | — |
| `keycloak.local` | Keycloak SSO | https://keycloak.local | `admin` / Vault |
| `vault.local` | Vault | http://vault.local:8200 | root token from `vault-init.json` |

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

### Vault Management (runs on host)

```bash
k8s-provisioner vault status        # Status do Vault (inicializado, sealed/unsealed)
k8s-provisioner vault init-info     # Como recuperar as credenciais do storage node
k8s-provisioner vault get-secret <path>  # Lê um secret (requer VAULT_TOKEN)
```

Exemplo:
```bash
export VAULT_TOKEN=hvs.xxxx
./build/k8s-provisioner-darwin-arm64 vault get-secret k8s-provisioner/api-keys
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
  kubernetes: "1.34"
  crio: "v1.34"
  calico: "3.31.5"
  metallb: "0.15.3"
  istio: "1.29.2"

network:
  interface: "eth1"
  controlplane_ip: "192.168.56.10"
  metallb_range: "192.168.56.200-192.168.56.250"

storage:
  nfs_server: "storage"
  nfs_path: "/exports/k8s-volumes"
  default_dynamic: true

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
  karpor: "none"                  # Options: enabled, none (desabilitado por padrão — consome ~1.5 CPU, ~2GB RAM)

# HashiCorp Vault (roda no storage node, fora do cluster)
vault:
  enabled: true
  address: "http://192.168.56.20:8200"
  version: "2.0.0"
  auto_init: true    # Inicializa e faz unseal automaticamente
  k8s_auth: true     # Configura Kubernetes auth method para pods

# Karpor AI (requer karpor: "enabled" acima)
karpor_ai:
  enabled: false
  backend: "ollama"
  model: "llama3.2:3b"

# Ollama (requer karpor_ai.enabled: true)
# API key para modelos cloud — se Vault habilitado, armazene lá e deixe vazio aqui
ollama:
  api_key: ""
```

### vagrant/settings.yaml

```yaml
box_name: "bento/debian-13"
vm:
# Storage VM (NFS Server + Vault) - must be created first
- name: "storage"
  ip: "192.168.56.20"
  memory: "2048"    # 2GB: NFS server + HashiCorp Vault
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
  memory: "8192"    # Extra for AI workloads (Ollama, se habilitado)
  cpus: "2"
  role: "worker"
- name: "node02"
  ip: "192.168.56.12"
  memory: "4096"
  cpus: "2"
  role: "worker"
```

## HashiCorp Vault

O Vault é instalado automaticamente no storage node (`192.168.56.20:8200`) como serviço systemd, **fora do cluster Kubernetes**. Durante o `provision all`, o provisioner:

1. Inicializa o Vault (5 unseal keys, threshold 3) e faz unseal automático
2. Salva as credenciais em `/etc/vault.d/vault-init.json` no storage node
3. Habilita o **KV v2 secrets engine** em `secret/k8s-provisioner/`
4. Configura o **Kubernetes auth method** para pods se autenticarem via ServiceAccount
5. Gera e armazena a **senha do Grafana** aleatoriamente
6. Armazena **API keys** do Ollama e Karpor (se configurados)

### Recuperando credenciais do Vault

As credenciais são salvas em dois locais durante o `provision all`:

| Local | Path | Acesso |
|-------|------|--------|
| Storage node | `/etc/vault.d/vault-init.json` | `vagrant ssh storage` |
| Controlplane (backup) | `/etc/k8s-provisioner/vault-init.json` | `vagrant ssh controlplane` |

**Opção 1 — via storage node (principal):**
```bash
vagrant ssh storage -c 'sudo cat /etc/vault.d/vault-init.json' | jq .
```

**Opção 2 — via controlplane (backup):**
```bash
vagrant ssh controlplane -c 'sudo cat /etc/k8s-provisioner/vault-init.json' | jq .
```

**Opção 3 — via CLI do projeto (na máquina host):**
```bash
./build/k8s-provisioner-darwin-arm64 vault init-info
```

O JSON retornado contém:
```json
{
  "keys": ["unseal-key-1", "unseal-key-2", "..."],
  "root_token": "hvs.XXXXXXXXXXXXXXXX"
}
```

Use o `root_token` para acessar a UI ou autenticar no CLI.

### Acessar a UI

```
http://192.168.56.20:8200/ui
```

Faça login com o `root_token` obtido acima.

### Recuperar senha do Grafana

```bash
# Via CLI do projeto
./build/k8s-provisioner-darwin-arm64 vault get-secret k8s-provisioner/api-keys

# Via Vault CLI
export VAULT_ADDR=http://192.168.56.20:8200
export VAULT_TOKEN=$(vagrant ssh storage -c 'sudo cat /etc/vault.d/vault-init.json' | jq -r .root_token)
vault kv get -field=grafana_admin_password secret/k8s-provisioner/api-keys
```

### Verificar status

```bash
./build/k8s-provisioner-darwin-arm64 vault status
```

### Operações via Vault CLI

Configure as variáveis de ambiente antes de usar o Vault CLI:

```bash
export VAULT_ADDR=http://192.168.56.20:8200
export VAULT_TOKEN=$(vagrant ssh storage -c 'sudo cat /etc/vault.d/vault-init.json' | jq -r .root_token)
```

#### Listar e ler secrets

```bash
# Listar todos os paths de secrets
vault kv list secret/k8s-provisioner/

# Ler todos os secrets do projeto
vault kv get secret/k8s-provisioner/api-keys

# Ler um campo específico
vault kv get -field=grafana_admin_password secret/k8s-provisioner/api-keys
vault kv get -field=ollama_api_key secret/k8s-provisioner/api-keys
```

#### Criar e atualizar secrets

```bash
# Adicionar um novo secret (mantém os existentes)
vault kv patch secret/k8s-provisioner/api-keys meu_secret="valor"

# Substituir todos os secrets de um path
vault kv put secret/k8s-provisioner/api-keys \
  grafana_admin_password="nova-senha" \
  ollama_api_key="olka_xxxxx"

# Criar um path novo
vault kv put secret/minha-app/config \
  db_password="senha-segura" \
  api_key="chave-api"
```

#### Versões e histórico

```bash
# Ver histórico de versões de um secret
vault kv metadata get secret/k8s-provisioner/api-keys

# Ler uma versão específica
vault kv get -version=1 secret/k8s-provisioner/api-keys

# Restaurar versão anterior
vault kv undelete -versions=1 secret/k8s-provisioner/api-keys
```

#### Gerenciar políticas

```bash
# Listar políticas
vault policy list

# Ver política existente
vault policy read k8s-provisioner

# Criar política personalizada
vault policy write minha-app - <<EOF
path "secret/data/minha-app/*" {
  capabilities = ["read"]
}
path "secret/data/k8s-provisioner/api-keys" {
  capabilities = ["read"]
}
EOF
```

#### Kubernetes auth method

```bash
# Verificar configuração do k8s auth
vault auth list
vault read auth/kubernetes/config

# Listar roles configuradas
vault list auth/kubernetes/role

# Ver detalhes de uma role
vault read auth/kubernetes/role/k8s-provisioner

# Criar role para uma nova aplicação
vault write auth/kubernetes/role/minha-app \
  bound_service_account_names="minha-app-sa" \
  bound_service_account_namespaces="default" \
  policies="minha-app" \
  ttl="1h"
```

#### Testar autenticação de um pod

```bash
# Dentro do pod — autentica usando o ServiceAccount token
SA_TOKEN=$(cat /var/run/secrets/kubernetes.io/serviceaccount/token)

vault write auth/kubernetes/login \
  role="k8s-provisioner" \
  jwt="$SA_TOKEN"
```

### App de exemplo

```bash
# Deploy de uma app que busca secrets do Vault via init container
kubectl apply -f examples/vault-secret-app.yaml

# Acompanhar autenticação e busca do secret
kubectl logs -l app=vault-demo -c vault-fetch-secrets

# Ver o container principal usando o secret
kubectl logs -l app=vault-demo -c app

# Inspecionar o arquivo de secret (em memória)
kubectl exec -it deploy/vault-demo-app -- cat /vault/secrets/api-keys.json

# Remover
kubectl delete -f examples/vault-secret-app.yaml
```

Veja [examples/vault-usage.md](examples/vault-usage.md) para o guia completo.

### Fluxo de secrets

```
config.yaml (apenas na primeira vez)
    └─► VaultInstaller armazena em Vault
              ├─► grafana_admin_password (gerada aleatoriamente)
              ├─► ollama_api_key
              └─► karpor_auth_token

              ↓ provisioner continua

    Monitoring ──► lê grafana_admin_password do Vault
                       └─► cria K8s Secret grafana-admin
                               └─► Grafana usa secretKeyRef

    Ollama ──────► lê ollama_api_key do Vault
                       └─► cria K8s Secret ollama-api-key
                               └─► Pod usa secretKeyRef

    Karpor ──────► lê karpor_auth_token do Vault
                       └─► passa via helm --set (sem arquivo em disco)
```

Após o primeiro deploy, os secrets ficam **apenas no Vault**. Remova as chaves do `config.yaml`:

```yaml
ollama:
  api_key: ""   # vazio — o Vault é a fonte de verdade agora
```

### Boas práticas

| Prática | Por quê |
|---------|---------|
| Nunca commitar `config.yaml` com API keys preenchidas | Evita exposição acidental no git |
| Usar o Vault como única fonte de verdade após o primeiro deploy | Garante rastreabilidade e rotação centralizada |
| Criar políticas com menor privilégio por aplicação | Um secret comprometido não expõe os demais |
| Usar `kv patch` para atualizar sem sobrescrever outros campos | Preserva o histórico de versões do KV v2 |
| Nunca usar o root token em aplicações | Crie tokens scoped via `vault token create -policy=minha-app` |
| Armazenar secrets apenas em volumes `emptyDir.medium: Memory` | Secrets nunca vão para disco nas VMs |
| Rotacionar o root token após o setup inicial | `vault token revoke <root_token>` após criar tokens de operação |

### Rotacionar a senha do Grafana

```bash
# Gerar nova senha e salvar no Vault
vault kv patch secret/k8s-provisioner/api-keys \
  grafana_admin_password="nova-senha-segura"

# Atualizar o K8s Secret (Grafana usa no próximo restart)
kubectl create secret generic grafana-admin \
  -n monitoring \
  --from-literal=password="nova-senha-segura" \
  --dry-run=client -o yaml | kubectl apply -f -

# Reiniciar o Grafana para aplicar
kubectl rollout restart deployment/grafana -n monitoring
```

### Desabilitar o Vault

```yaml
# config.yaml
vault:
  enabled: false
```

Nesse caso o Grafana usa a senha padrão `admin123` e as API keys vêm diretamente do `config.yaml`.

---

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

## Autoscaling

The cluster includes three complementary autoscalers. They can be used independently or together.

| Autoscaler | What it scales | Trigger | Config |
|------------|---------------|---------|--------|
| **HPA** | Replicas (horizontal) | CPU, memory, custom metrics | Native Kubernetes |
| **VPA** | CPU/Memory requests per pod (vertical) | Historical usage | `components.vpa: "enabled"` |
| **KEDA** | Replicas to zero and back | Prometheus, queues, cron, etc. | `components.keda: "enabled"` |

### HPA (Horizontal Pod Autoscaler)

Scales the number of pod replicas based on CPU or memory. Requires Metrics Server (included).

```bash
# Scale based on CPU (50% target, 1-10 replicas)
kubectl autoscale deployment my-app --cpu-percent=50 --min=1 --max=10

# Or declaratively
kubectl apply -f - <<EOF
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: my-app
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: my-app
  minReplicas: 1
  maxReplicas: 10
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 50
EOF

# Check HPA status
kubectl get hpa
kubectl describe hpa my-app
```

### VPA (Vertical Pod Autoscaler)

Automatically adjusts CPU and memory **requests** for each pod based on observed usage. Useful when you don't know the right resource requests upfront.

> VPA and HPA should not be used together on the same metric (e.g. both on CPU). Use VPA for right-sizing, HPA for scaling replicas.

```bash
# Check VPA components
kubectl get pods -n kube-system | grep vpa

# Create a VPA for a deployment
kubectl apply -f - <<EOF
apiVersion: autoscaling.k8s.io/v1
kind: VerticalPodAutoscaler
metadata:
  name: my-app-vpa
  namespace: default
spec:
  targetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: my-app
  updatePolicy:
    updateMode: "Auto"   # Options: Auto, Recreate, Initial, Off
  resourcePolicy:
    containerPolicies:
    - containerName: my-app
      minAllowed:
        cpu: 50m
        memory: 64Mi
      maxAllowed:
        cpu: 2
        memory: 2Gi
EOF

# Check VPA recommendations (even in Off mode)
kubectl describe vpa my-app-vpa
```

**Update modes:**

| Mode | Behaviour |
|------|-----------|
| `Off` | Only shows recommendations, never applies them |
| `Initial` | Sets requests only at pod creation, never evicts |
| `Recreate` | Evicts pods to apply new requests |
| `Auto` | Same as Recreate (default recommended) |

### KEDA (Kubernetes Event-Driven Autoscaler)

Scales deployments based on external event sources — including Prometheus metrics. Can scale to **zero** when there is no load.

```bash
# Check KEDA components
kubectl get pods -n keda
```

#### Scale on Prometheus metric

```yaml
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: my-app-scaler
  namespace: default
spec:
  scaleTargetRef:
    name: my-app
  minReplicaCount: 0    # scales to zero when idle
  maxReplicaCount: 10
  triggers:
  - type: prometheus
    metadata:
      serverAddress: http://prometheus.monitoring.svc:9090
      metricName: http_requests_total
      threshold: "100"       # scale up when > 100 req/s per replica
      query: sum(rate(http_requests_total{app="my-app"}[2m]))
```

#### Scale on CPU/Memory (via Prometheus)

```yaml
triggers:
- type: prometheus
  metadata:
    serverAddress: http://prometheus.monitoring.svc:9090
    metricName: cpu_usage
    threshold: "70"
    query: |
      avg(rate(container_cpu_usage_seconds_total{pod=~"my-app-.*"}[2m])) * 100
```

#### Scale on cron schedule

```yaml
triggers:
- type: cron
  metadata:
    timezone: America/Sao_Paulo
    start: "0 8 * * 1-5"    # weekdays at 08:00
    end: "0 18 * * 1-5"     # weekdays at 18:00
    desiredReplicas: "3"
```

```bash
# Check ScaledObject status
kubectl get scaledobject
kubectl describe scaledobject my-app-scaler
```

## Observability

This project implements the three pillars of observability, following the observability pyramid model where each layer builds on the previous.

### The Observability Pyramid

```
        ╔══════════════════════╗
        ║       TRACES         ║  ← Why is it slow / where did it fail?
        ║   (Grafana Tempo +   ║
        ║    OpenTelemetry)    ║
        ╠══════════════════════╣
        ║        LOGS          ║  ← What happened?
        ║  (Loki + Alloy)      ║
        ╠══════════════════════╣
        ║       METRICS        ║  ← Is something wrong?
        ║ (Prometheus+Grafana) ║
        ╚══════════════════════╝
```

> The pyramid reflects signal volume and cost: metrics are cheap and always-on; logs are richer but larger; traces are the most detailed and selectively sampled.

### Three Pillars — Project Coverage

| Pillar | Tool(s) | What it answers | Status |
|--------|---------|-----------------|--------|
| **Metrics** | Prometheus + Grafana + Node Exporter + kube-state-metrics | Is anything wrong? What are the SLIs? | ✅ Enabled by default |
| **Logs** | Loki 3.x + Grafana Alloy | What happened and when? Which pod crashed? | ✅ Enabled with monitoring |
| **Traces** | Grafana Tempo + OpenTelemetry Collector | Why is a request slow? Which service failed? | ✅ Enabled with `tracing: otel-tempo` |
| **Mesh Visibility** | Kiali | How are services connected? What is the error rate between them? | ✅ Enabled with monitoring |

### Cross-Signal Correlation (Grafana)

| From | To | Trigger |
|------|----|---------|
| Log line with `traceID=` | Trace in Tempo | Click derived field link |
| Trace span | Logs for that service | "Logs" button in Tempo UI |
| Trace span | RED metrics in Prometheus | `service.name` tag link |
| Service Map (Tempo) | Prometheus rate/error/duration | Click node in graph |

---

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

### /etc/hosts Setup (Mac/Linux host)

All web UIs are exposed via the same Istio Ingress Gateway IP. Add all entries at once:

```bash
INGRESS_IP=$(kubectl get svc -n istio-system istio-ingressgateway -o jsonpath='{.status.loadBalancer.ingress[0].ip}')

sudo tee -a /etc/hosts <<EOF
$INGRESS_IP grafana.local
$INGRESS_IP prometheus.local
$INGRESS_IP alertmanager.local
$INGRESS_IP kiali.local
$INGRESS_IP karpor.local
EOF
```

> Run this once from your Mac/Linux host, not inside the VMs.

---

### Accessing Grafana

Grafana is exposed via Istio Ingress Gateway:

```bash
# Get Istio Ingress IP
INGRESS_IP=$(kubectl get svc -n istio-system istio-ingressgateway -o jsonpath='{.status.loadBalancer.ingress[0].ip}')

# Add to /etc/hosts (if not done already in the setup section above)
echo "$INGRESS_IP grafana.local" | sudo tee -a /etc/hosts

# Access
open https://grafana.local
```

**Credentials:**
- Username: `admin`
- Password: gerada pelo Vault — recupere com:
  ```bash
  vault kv get -field=grafana_admin_password secret/k8s-provisioner/api-keys
  ```
  > Se Vault desabilitado: `admin123`

### Accessing Prometheus

```bash
open https://prometheus.local
```

### Accessing Alertmanager

```bash
open https://alertmanager.local
```

**Check active alerts:**
```bash
kubectl port-forward -n monitoring svc/alertmanager 9093:9093
# Access: http://localhost:9093
```

**Configure notifications (Slack, email, PagerDuty):**

With Vault enabled, store the full `alertmanager.yaml` config:
```bash
export VAULT_TOKEN=$(vagrant ssh storage -c 'sudo cat /etc/vault.d/vault-init.json' | jq -r .root_token)
vault kv patch secret/k8s-provisioner/api-keys alertmanager_config=@alertmanager.yaml
```

Without Vault, edit the secret directly:
```bash
kubectl edit secret alertmanager-alertmanager -n monitoring
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

#### cert-manager Dashboards

| ID | Dashboard | Description |
|----|-----------|-------------|
| `20842` | cert-manager | Certificate expiry, renewal, and issuer status |

> **Note:** The cert-manager ServiceMonitor is automatically created during provisioning. The dashboard shows all certificates managed by cert-manager, including expiry dates and renewal status.

## Logging Stack (Loki)

The cluster includes Loki 3.x for log aggregation with Grafana Alloy as the log collector (replaces the deprecated Promtail).

### Storage Recommendation by Environment

| Environment | Storage for Loki/Tempo | Reason |
|-------------|----------------------|--------|
| **Lab/Dev** (this project) | NFS dynamic | Simple, zero configuration |
| **On-premise production** | Ceph (Rook-Ceph) or Longhorn | Distributed block storage, HA, replication |
| **Cloud production** | S3 / GCS / Azure Blob | Loki and Tempo have native object storage support |
| **Hybrid production** | MinIO (S3-compatible) | Self-hosted object storage with S3 API |

> Loki 3.x and Tempo were designed to use object storage natively in production. NFS works well for lab environments but lacks redundancy — if the storage node goes down, both Loki and Tempo stop working.

### Components

| Component | Description |
|-----------|-------------|
| Loki 3.x | Log aggregation and storage (TSDB schema v13) |
| Grafana Alloy | Log collector DaemonSet — replaces Promtail, collects pod logs and Kubernetes events |

### Accessing Logs

Logs are accessed via Grafana:

1. Open **https://grafana.local**
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
| `12611` | Loki & Alloy | Logs with Alloy stats |
| `15141` | Loki Logs | Simple log viewer |

## Tracing Stack (OpenTelemetry + Grafana Tempo)

Enabled via `components.tracing: "otel-tempo"`, this stack adds distributed tracing to complete the three pillars of observability.

### Components

| Component | Description | Port |
|-----------|-------------|------|
| Grafana Tempo | Trace storage and query backend | 3200 (HTTP), 4317 (OTLP gRPC), 4318 (OTLP HTTP) |
| OpenTelemetry Collector | DaemonSet that receives and forwards traces | 4317/4318 (hostPort) |

### Automatic Trace Injection (Zero Code Changes)

When `tracing: otel-tempo` is enabled, the provisioner automatically configures **two layers** of telemetry for all deployments in the cluster:

#### Layer 1 — Istio Mesh Tracing (always active)

The Istio sidecar proxy (Envoy) is configured to forward HTTP/gRPC traces to the OTel Collector. This happens automatically for **every namespace** with `istio-injection: enabled` — including the example apps (`hello`, `podinfo`).

```
App Pod  →  Envoy Sidecar  →  OTel Collector  →  Grafana Tempo
            (auto-injected)    (DaemonSet)
```

What you get for free:
- Inbound/outbound HTTP spans with latency, status code, method, URL
- Service-to-service call graph (Service Map in Grafana)
- 100% sampling rate (configurable in `Telemetry` resource)

#### Layer 2 — OTel Operator (deep code instrumentation, opt-in)

For code-level spans (database queries, function calls, custom business logic), deploy the OTel Operator and annotate your pod with a single line:

```yaml
annotations:
  instrumentation.opentelemetry.io/inject-java: "true"   # Java
  instrumentation.opentelemetry.io/inject-nodejs: "true" # Node.js
  instrumentation.opentelemetry.io/inject-python: "true" # Python
  instrumentation.opentelemetry.io/inject-go: "true"     # Go
```

See `examples/otel-operator-instrumentation.yaml` for a complete example.

#### Sending Traces from Your Apps (manual SDK)

For custom instrumentation or languages not supported by the Operator:

```bash
# From inside the cluster (via Service)
OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector.monitoring.svc:4317   # gRPC
OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector.monitoring.svc:4318   # HTTP

# From any node (via DaemonSet hostPort)
OTEL_EXPORTER_OTLP_ENDPOINT=http://<node-ip>:4317
```

### Viewing Traces in Grafana

1. Open **https://grafana.local**
2. Go to **Explore** → select **Tempo** as datasource
3. Search by **TraceID**, service name, or use the **Service Graph**

### Observability Correlations

The stack is configured with fixed datasource UIDs to enable cross-signal navigation:

| From | To | How |
|------|----|-----|
| Trace → Logs | Tempo → Loki | Click TraceID in a log line to jump to the trace |
| Trace → Metrics | Tempo → Prometheus | `service.name` tag links to RED metrics |
| Service Map | Prometheus | Auto-generated from span metrics |
| Node Graph | Tempo | Visualize trace topology |

### Go Example (OpenTelemetry SDK)

```go
import (
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
    "go.opentelemetry.io/otel/sdk/trace"
)

exporter, _ := otlptracegrpc.New(ctx,
    otlptracegrpc.WithEndpoint("otel-collector.monitoring.svc:4317"),
    otlptracegrpc.WithInsecure(),
)
tp := trace.NewTracerProvider(trace.WithBatcher(exporter))
otel.SetTracerProvider(tp)
```

### Instrumenting a New Application

For full trace → log correlation to work in Grafana ("Log for this span" button in Tempo), your application must satisfy three requirements:

#### 1. Pod label `app: <name>`

Alloy (the log collector) automatically extracts the `app` label from pod labels and adds it to every log stream in Loki. Without it, Tempo cannot find the matching logs.

```yaml
# deployment.yaml
spec:
  template:
    metadata:
      labels:
        app: my-app   # required
```

#### 2. Log format with `traceID=<hex>`

Include the active trace ID in every log line using logfmt key `traceID`. Loki's derived fields use the regex `traceID=(\w+)` to extract it and create a clickable link to Tempo.

```go
// Go example — log inside a span handler
traceID := span.SpanContext().TraceID().String()
log.Printf("traceID=%s level=info msg=\"handled request\"", traceID)
```

```python
# Python example
import logging
trace_id = trace.get_current_span().get_span_context().trace_id
logging.info(f"traceID={trace_id:032x} level=info msg=handled_request")
```

```java
// Java example (SLF4J + OTel SDK)
import io.opentelemetry.api.trace.Span;

String traceId = Span.current().getSpanContext().getTraceId();
logger.info("traceID={} level=info msg=handled_request", traceId);
```

#### 3. Set `service.name` matching the `app` label

When initialising the OTel tracer, set the resource attribute `service.name` to the **same value** as your pod's `app` label. Tempo uses this to query Loki with `{app="my-app"}`.

```go
resource.NewWithAttributes(
    semconv.SchemaURL,
    semconv.ServiceName("my-app"),   // must match pod label app=my-app
)
```

#### Full checklist

| Requirement | Why |
|-------------|-----|
| Pod label `app: my-app` | Alloy adds it as Loki stream label |
| Log line contains `traceID=<hex>` | Loki derived field links log → trace |
| OTel resource `service.name=my-app` | Tempo queries `{app="my-app"}` in Loki |
| Namespace has `istio-injection: enabled` | Istio auto-injects sidecar for mesh tracing |
| OTLP endpoint `otel-collector.monitoring.svc:4317` | Collector forwards to Tempo |

See `examples/otel-demo-app/` for a complete working reference (Go app + `deploy.yaml`).

### Verifying Installation

```bash
# Check pods
kubectl get pods -n monitoring -l app=tempo
kubectl get pods -n monitoring -l app=otel-collector

# Check Tempo is receiving traces (via port-forward)
kubectl port-forward -n monitoring svc/tempo 3200:3200
curl http://localhost:3200/ready
```

## Istio Installation Profile

This project installs Istio using the **`default` profile**. Understanding the available profiles:

| Profile | Ingress Gateway | Egress Gateway | Telemetry | Recommended for |
|---------|----------------|----------------|-----------|-----------------|
| `minimal` | ❌ | ❌ | minimal | Istiod only, gateways managed separately |
| `default` | ✅ | ❌ | standard | **Production** — balanced resources and security |
| `demo` | ✅ | ✅ | verbose | Learning and demos |
| `ambient` | ✅ | ❌ | standard | Production (no sidecar, node-level proxy) |

### Why `default` and not `demo`?

The `demo` profile enables everything for easy exploration but is not production-safe:
- Verbose logging and telemetry impacts performance
- No resource limits configured — can saturate nodes
- Relaxed security settings

### Enabling Egress Gateway (production)

The `default` profile does not include an Egress Gateway. To control outbound traffic in production, enable it explicitly:

```yaml
# istio-operator.yaml
apiVersion: install.istio.io/v1alpha1
kind: IstioOperator
spec:
  profile: default
  components:
    egressGateways:
    - name: istio-egressgateway
      enabled: true
```

```bash
istioctl install -f istio-operator.yaml -y
```

---

## Kiali (Service Mesh Observability)

Kiali is the observability console for Istio. It visualizes the service mesh topology in real-time, shows traffic health between services, and validates Istio configuration.

Installed automatically alongside the monitoring stack — no extra configuration required.

### What Kiali Shows

| View | Description |
|------|-------------|
| **Service Graph** | Live topology of all services, with RPS, error rate and latency on each edge |
| **Traffic Metrics** | RED metrics (Rate, Errors, Duration) per workload |
| **Config Validation** | Detects misconfigured VirtualServices, DestinationRules, Gateways |
| **Workload Details** | Pod health, logs (via Loki), traces (via Tempo) for each workload |
| **Istio Config** | Full view and edit of all Istio CRDs in the cluster |

### Accessing Kiali

```bash
# Get Istio Ingress IP
INGRESS_IP=$(kubectl get svc -n istio-system istio-ingressgateway -o jsonpath='{.status.loadBalancer.ingress[0].ip}')

# Add to /etc/hosts
echo "$INGRESS_IP kiali.local" | sudo tee -a /etc/hosts

# Access (no login required — anonymous mode)
open https://kiali.local/kiali
```

### Integrations

Kiali is pre-configured to link to the full observability stack:

| Signal | Backend | How to access |
|--------|---------|---------------|
| Metrics | Prometheus | Auto-loaded in service graphs and dashboards |
| Logs | Loki | Click "Show Logs" on any workload |
| Traces | Grafana Tempo | Click "Show Traces" on any workload or span link |
| Dashboards | Grafana | "View in Grafana" button on metrics panels |

### Verifying Installation

```bash
kubectl get pods -n istio-system -l app=kiali
kubectl get svc -n istio-system kiali
```

### Recommended Grafana Dashboards for Istio & Mesh Observability

> **Import tip:** When importing from grafana.com, Grafana asks you to map the `$datasource` variable to your Prometheus instance. Always select **Prometheus** from the dropdown — this is what causes the error if left blank or mismatched.

#### Option A — Import from Grafana.com (requires internet access)

**Dashboards** → **Import** → Enter ID → **Load** → Set datasource to **Prometheus** → **Import**

| ID | Dashboard | What it shows |
|----|-----------|---------------|
| `7639` | Istio Mesh Dashboard | Traffic overview: RPS, error rate, latency for the whole mesh |
| `7636` | Istio Service Dashboard | Per-service breakdown: inbound/outbound, success rate |
| `7630` | Istio Workload Dashboard | Per-pod/deployment metrics |
| `7645` | Istio Control Plane | istiod CPU, memory, xDS push latency |
| `11829` | Istio Performance | Envoy proxy performance metrics |
| `15983` | OpenTelemetry Collector | Spans received, exported, queue depth |

#### Option B — Import official JSON directly (recommended, works offline)

Download and import the Istio dashboards from the official Istio repo — these are pre-configured for Prometheus and don't have datasource UID issues:

```bash
# Download official Istio dashboards (pre-configured for Prometheus)
ISTIO_VERSION=release-1.29
BASE_URL=https://raw.githubusercontent.com/istio/istio/${ISTIO_VERSION}/manifests/addons/dashboards

for dashboard in istio-mesh-dashboard istio-service-dashboard istio-workload-dashboard \
                 istio-performance-dashboard istio-extension-dashboard; do
  curl -sSL "${BASE_URL}/${dashboard}.json" -o "/tmp/${dashboard}.json"
  echo "Downloaded: ${dashboard}.json"
done

# Import each file via Grafana UI:
# Dashboards → Import → Upload JSON file → select file → Import
```

After download, import each JSON file manually in Grafana: **Dashboards → Import → Upload JSON file**.

For the OTel Collector dashboard (ID `15983`), it uses a separate `$otelcol_datasource` variable — set it to **Prometheus** on import.

## Karpor (Kubernetes Explorer)

Karpor is a Kubernetes Explorer that provides intelligent search and AI-powered analysis.

> **Note:** Karpor está **desabilitado por padrão** pois requer recursos extras (~1.5 CPU, ~2GB RAM). Para habilitar:
> ```yaml
> components:
>   karpor: "enabled"
> karpor_ai:
>   enabled: true
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
open https://karpor.local
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

> **Note:** Ollama está **desabilitado por padrão** e só é instalado quando Karpor AI está habilitado:
> ```yaml
> karpor_ai:
>   enabled: true   # Habilita Ollama automaticamente
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
  model: "minimax-m2.5:cloud"

ollama:
  api_key: "olka_your_key_here"  # Apenas no primeiro deploy — depois armazene no Vault
```

> Se o Vault estiver habilitado, armazene a API key diretamente no Vault e deixe `api_key: ""` no config.yaml:
> ```bash
> vault kv patch secret/k8s-provisioner/api-keys ollama_api_key="olka_sua_chave"
> ```

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
kubectl apply -n demo -f https://raw.githubusercontent.com/istio/istio/release-1.29/samples/httpbin/httpbin.yaml

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

## HashiCorp Vault (Secrets Management)

Vault is installed automatically on the **storage node** (`192.168.56.20`) during cluster provisioning. It stores all sensitive credentials used by the cluster components.

### What Vault manages

| Secret path | Key | Used by |
|---|---|---|
| `secret/k8s-provisioner/api-keys` | `grafana_admin_password` | Grafana login |
| `secret/k8s-provisioner/api-keys` | `keycloak_admin_username` | Keycloak admin username |
| `secret/k8s-provisioner/api-keys` | `keycloak_admin_password` | Keycloak admin console |
| `secret/k8s-provisioner/api-keys` | `keycloak_postgres_username` | PostgreSQL username |
| `secret/k8s-provisioner/api-keys` | `keycloak_postgres_password` | PostgreSQL (Keycloak DB) |
| `secret/k8s-provisioner/api-keys` | `keycloak_grafana_client_secret` | Grafana OAuth2 client |
| `secret/k8s-provisioner/api-keys` | `keycloak_k8sadmin_password` | Realm user `k8sadmin` |
| `secret/k8s-provisioner/api-keys` | `keycloak_developer_password` | Realm user `developer` |

### Getting the Vault token

The init data (root token + unseal keys) is saved automatically to both nodes during provisioning:

```bash
# On the controlplane node
sudo cat /etc/k8s-provisioner/vault-init.json

# On the storage node
vagrant ssh storage -c 'sudo cat /etc/k8s-provisioner/vault-init.json'
```

Output example:
```json
{
  "keys": [
    "a4062b2811b...",
    "1c8fc4973e5...",
    "cfb91c7481a...",
    "04d1dead7c6...",
    "150414ae84d..."
  ],
  "root_token": "hvs.XXXXXXXXXXXXXXXXXXXXXXXXXXXX"
}
```

### Accessing the Vault UI

```bash
# vault.local requires the /etc/hosts entry: 192.168.56.20 vault.local
open http://vault.local:8200

# Or use the IP directly (no /etc/hosts needed)
open http://192.168.56.20:8200
```

Login with the `root_token` from `vault-init.json`.

### Accessing Vault via CLI

```bash
export VAULT_ADDR=http://192.168.56.20:8200
export VAULT_TOKEN=hvs.XXXXXXXXXXXXXXXXXXXXXXXXXXXX   # from vault-init.json

# List secrets
vault kv list secret/k8s-provisioner

# Read a secret
vault kv get secret/k8s-provisioner/api-keys

# Update Grafana password
vault kv patch secret/k8s-provisioner/api-keys grafana_admin_password=NewPassword123
```

> **Note:** After updating a password in Vault, restart the affected component (e.g. `kubectl rollout restart deployment/grafana -n monitoring`).

### Re-sealing and unsealing

Vault is automatically unsealed during provisioning. If the storage node is restarted, Vault will be sealed again. To unseal manually:

```bash
vagrant ssh storage

# Unseal with 3 of the 5 keys from vault-init.json
sudo VAULT_ADDR=http://localhost:8200 vault operator unseal <key-1>
sudo VAULT_ADDR=http://localhost:8200 vault operator unseal <key-2>
sudo VAULT_ADDR=http://localhost:8200 vault operator unseal <key-3>
```

### config.yaml

```yaml
vault:
  addr: "http://192.168.56.20:8200"
  token: ""   # Leave empty — token is auto-read from /etc/k8s-provisioner/vault-init.json
```

---

## Keycloak (Identity Provider / SSO)

Keycloak provides OIDC authentication for `kubectl` and Single Sign-On (SSO) for Grafana. It runs inside the Kubernetes cluster with a PostgreSQL backend.

### Access

```bash
# NodePort — accessible without /etc/hosts
open http://192.168.56.10:30080

# Or via Istio Gateway (requires /etc/hosts entry — see Quick Start step 5)
open https://keycloak.local
```

### Admin credentials

```
URL:      http://192.168.56.10:30080  (or https://keycloak.local via Istio)
Username: admin
Password: (from Vault → secret/k8s-provisioner/api-keys → keycloak_admin_password)
```

All Keycloak credentials are stored in Vault at `secret/k8s-provisioner/api-keys`:

| Vault key | Description |
|-----------|-------------|
| `keycloak_admin_username` | Admin console username (`admin`) |
| `keycloak_admin_password` | Admin console password |
| `keycloak_postgres_username` | PostgreSQL username (`keycloak`) |
| `keycloak_postgres_password` | PostgreSQL password |
| `keycloak_grafana_client_secret` | Grafana OIDC client secret |
| `keycloak_k8sadmin_password` | Realm user `k8sadmin` password |
| `keycloak_developer_password` | Realm user `developer` password |

To customize passwords before provisioning:

```bash
export VAULT_ADDR=http://192.168.56.20:8200
export VAULT_TOKEN=$(cat /etc/k8s-provisioner/vault-init.json | jq -r .root_token)

vault kv patch secret/k8s-provisioner/api-keys \
  keycloak_k8sadmin_password="MinhaSenh@Forte" \
  keycloak_developer_password="OutraSenha@123"
```

> **Note:** Run this after Vault is initialized but before Keycloak is installed (or recreate the cluster).

To retrieve via Vault UI: `http://vault.local:8200` → **secret/k8s-provisioner/api-keys**

To retrieve via CLI:
```bash
export VAULT_ADDR=http://192.168.56.20:8200
export VAULT_TOKEN=$(cat /etc/k8s-provisioner/vault-init.json | grep root_token | cut -d'"' -f4)
vault kv get secret/k8s-provisioner/api-keys
```

### Pre-configured realm: `k8s`

| Resource | Details |
|---|---|
| Realm | `k8s` |
| kubectl client | `kubectl` (public, PKCE enabled) |
| Grafana client | `grafana` (confidential) |
| Admin group | `k8s-admins` → `cluster-admin` RBAC |
| Developer group | `k8s-developers` → `view` RBAC |
| Test user (admin) | `k8sadmin` / (Vault: `keycloak_k8sadmin_password`) |
| Test user (dev) | `developer` / (Vault: `keycloak_developer_password`) |

### kubectl access

#### Quick access (admin)

Two commands to get full cluster access from your Mac:

```bash
# 1. Copy kubeconfig and fix the API server address
vagrant ssh controlplane -c "cat /etc/kubernetes/admin.conf" \
  | sed 's|https://.*:6443|https://192.168.56.10:6443|' \
  > ~/.kube/k8s-lab.conf

# 2. Use it
export KUBECONFIG=~/.kube/k8s-lab.conf
kubectl get nodes
```

This uses the `cluster-admin` credentials. Suitable for day-to-day lab usage.

#### OIDC login via Keycloak (kubelogin)

Use this for role-based access — each user logs in with their own Keycloak credentials and gets the permissions of their group (`k8s-admins` → `cluster-admin`, `k8s-developers` → `view`).

The `kubeconfig-oidc` file contains no credentials — it is safe to distribute. It is stored in Vault automatically during provisioning.

**Admin: adding a new user**

Access is group-based — no Kubernetes changes needed. Just create the user in Keycloak and assign the group:

| Keycloak group | Kubernetes access | Grafana role |
|---------------|-------------------|--------------|
| `k8s-admins` | `cluster-admin` | Admin |
| `k8s-developers` | `view` (read-only) | Viewer |

**Option A — Keycloak Admin Console:**

1. Open `https://keycloak.local/admin` → realm `k8s` → Users → Add user
2. Set username, email, enable the user, save
3. Go to **Credentials** tab → Set password
4. Go to **Groups** tab → Join `k8s-admins` or `k8s-developers`

**Option B — CLI (from controlplane):**

```bash
# Get admin token
KCADM="kubectl exec -n keycloak deploy/keycloak -- /opt/keycloak/bin/kcadm.sh"
$KCADM config credentials --server http://localhost:8080 --realm master \
  --user "$KEYCLOAK_ADMIN" --password "$KEYCLOAK_ADMIN_PASSWORD"

# Create user and add to group
NEW_UID=$($KCADM create users -r k8s \
  -s username=alice \
  -s email=alice@example.com \
  -s firstName=Alice \
  -s lastName=Smith \
  -s enabled=true -i)
$KCADM set-password -r k8s --username alice --new-password 'Alice@K8s123'

GID=$($KCADM get groups -r k8s | grep -A1 k8s-developers | grep id | cut -d'"' -f4)
$KCADM update users/$NEW_UID/groups/$GID -r k8s -s realm=k8s -s userId=$NEW_UID -s groupId=$GID -n
```

5. Retrieve the `kubeconfig-oidc` from Vault and send it to the user:

```bash
vagrant ssh storage

export VAULT_ADDR=http://127.0.0.1:8200
export VAULT_TOKEN=$(sudo cat /etc/vault.d/vault-init.json | grep root_token | cut -d'"' -f4)

vault kv get -field=config secret/k8s-provisioner/kubeconfig-oidc > /tmp/kubeconfig-oidc
exit
```

Copy to your Mac:

```bash
vagrant scp storage:/tmp/kubeconfig-oidc ./kubeconfig-oidc
```

Send the `kubeconfig-oidc` file to the user.

**Alternative: copy from the Vault UI**

1. Open `http://192.168.56.20:8200` in your browser and log in with the root token
2. Navigate to **secret → k8s-provisioner → kubeconfig-oidc**
3. Copy the value of the `config` field
4. Paste into a new file and save as `~/.kube/kubeconfig-oidc`

**New user: first-time setup (once)**

```bash
# 1. Install kubelogin
brew install int128/kubelogin/kubelogin   # macOS

# 2. Add keycloak.local to /etc/hosts (required to reach the OIDC issuer)
INGRESS_IP=$(vagrant ssh controlplane -c "kubectl get svc -n istio-system istio-ingressgateway -o jsonpath='{.status.loadBalancer.ingress[0].ip}'" 2>/dev/null | tr -d '\r')
echo "$INGRESS_IP keycloak.local" | sudo tee -a /etc/hosts

# 3. Save the kubeconfig received from admin
mkdir -p ~/.kube
cp kubeconfig-oidc ~/.kube/k8s-lab.conf
export KUBECONFIG=~/.kube/k8s-lab.conf   # add to ~/.zshrc to persist

# 4. First login — opens browser at Keycloak
kubectl get nodes
```

Log in with your Keycloak credentials. The token is cached locally; the browser will not open again until it expires (24h).

Verify who you are authenticated as:

```bash
kubectl auth whoami
# ATTRIBUTE   VALUE
# Username    oidc:alice
# Groups      [oidc:k8s-developers system:authenticated]
```

**Day-to-day usage:**

```bash
export KUBECONFIG=~/.kube/k8s-lab.conf
kubectl get nodes
kubectl get pods -A
```

### Grafana SSO

Grafana is configured to use Keycloak for SSO. Click **"Sign in with Keycloak"** on the Grafana login page.

| User | Password | Grafana Role |
|------|----------|--------------|
| `k8sadmin` | `vault kv get -field=keycloak_k8sadmin_password secret/k8s-provisioner/api-keys` | Admin |
| `developer` | `vault kv get -field=keycloak_developer_password secret/k8s-provisioner/api-keys` | Viewer |

- Users in `k8s-admins` group → Grafana `Admin` role
- All other users → Grafana `Viewer` role
- Local admin login still works: `admin` / (password from Vault)

### Registering a new application in Keycloak

To protect a new application with Keycloak SSO, create a client in the `k8s` realm.

**Via Admin Console** (`https://keycloak.local/admin`):

1. Login with `admin` credentials
2. Select realm **k8s** (top-left dropdown)
3. Go to **Clients** → **Create client**
4. Fill in:
   - **Client type**: `OpenID Connect`
   - **Client ID**: your app name (e.g., `myapp`)
5. Enable **Client authentication** if your app can keep a secret (confidential client)
6. Set **Valid redirect URIs**: `https://myapp.local/*`
7. Set **Web origins**: `https://myapp.local`
8. Save

**Via kcadm (CLI):**

```bash
kubectl exec -n keycloak deployment/keycloak -- bash -c '
KCADM=/opt/keycloak/bin/kcadm.sh
$KCADM config credentials --server http://localhost:8080 \
  --realm master --user admin --password Admin@Keycloak123

# Confidential client (server-side apps)
$KCADM create clients -r k8s \
  -s clientId=myapp \
  -s publicClient=false \
  -s secret=my-client-secret \
  -s "redirectUris=[\"https://myapp.local/*\"]" \
  -s enabled=true

# Public client with PKCE (SPAs / CLIs)
$KCADM create clients -r k8s \
  -s clientId=myapp-spa \
  -s publicClient=true \
  -s "redirectUris=[\"https://myapp.local/*\"]" \
  -s "attributes={\"pkce.code.challenge.method\":\"S256\"}" \
  -s enabled=true
'
```

**Adding the groups claim to a new client:**

```bash
kubectl exec -n keycloak deployment/keycloak -- bash -c '
KCADM=/opt/keycloak/bin/kcadm.sh
$KCADM config credentials --server http://localhost:8080 \
  --realm master --user admin --password Admin@Keycloak123

# Get the groups scope ID
SCOPE_ID=$($KCADM get client-scopes -r k8s --fields id,name \
  | grep -B2 "\"groups\"" \
  | grep -oE "[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}" | head -1)

# Get your client ID
CLIENT_ID=$($KCADM get clients -r k8s --fields id,clientId \
  | grep -B2 "\"myapp\"" \
  | grep -oE "[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}" | head -1)

# Associate groups scope
$KCADM update clients/$CLIENT_ID/optional-client-scopes/$SCOPE_ID -r k8s
'
```

The token will then include `"groups": ["k8s-admins"]` (or whichever groups the user belongs to), which your app can use for role-based access control.

---

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
| Storage | 2 GB | 1 | 10 GB | NFS Server + HashiCorp Vault |
| ControlPlane | 6 GB | 4 | 20 GB | K8s Master + Monitoring |
| Node01 | 8 GB | 2 | 20 GB | Worker + AI Workloads (opcional) |
| Node02 | 4 GB | 2 | 20 GB | Worker |
| **Total** | **20 GB** | **9** | **70 GB** | |

> **Karpor + Ollama desabilitados por padrão.** Para habilitá-los, o Node01 precisa dos 8GB para carregar o modelo llama3.2:3b (~4GB RAM).

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