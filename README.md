<p align="center">
  <img src="docs/logo/k8s-provisioner-logo.svg" alt="k8s-provisioner" width="320">
</p>

# k8s-provisioner

Kubernetes cluster provisioner written in Go for lab environments. Supports macOS, Linux and Windows.

## Stack

| Component | Version |
|-----------|---------|
| OS | Debian 13 "Trixie" |
| Container Runtime | CRI-O 1.37 |
| Kubernetes | 1.37 |
| CNI | Calico 3.33.0 |
| LoadBalancer | MetalLB 0.16.0 |
| Service Mesh | Istio 1.31.3 + Kiali 2.33.0 |
| TLS / Certificates | cert-manager v1.21.2 |
| Storage | NFS Server + Dynamic Provisioner |
| Secrets Management | HashiCorp Vault 2.2.1 (TLS) |
| Lab DNS | CoreDNS 1.15.0 + etcd 3.7.3 (storage node) + ExternalDNS 0.23.0 |
| Secrets Sync | Vault Secrets Operator (VSO) 1.6 |
| Autoscaling | HPA (native) + VPA 1.8.1 + KEDA 2.21 |
| Metrics | Metrics Server + Prometheus Operator 0.94.1 |
| Monitoring | Prometheus 3.15.1 + Alertmanager 0.34.1 + Grafana 13.2.4 + node-exporter 1.12.1 + blackbox_exporter 0.29.1 |
| Lab dashboard | `k8s-lab` in Grafana: workload resources, DNS SLOs, DNS, certificate expiry |
| Logging | Loki 3.7.9 + Grafana Alloy 1.21.1 |
| Tracing | Grafana Tempo 3.1.1 + OpenTelemetry Collector 0.163.2 |
| Identity Provider | Keycloak 26.8.1 + PostgreSQL 18 |
| Kubernetes Explorer | Karpor 0.6.4 (chart 0.7.8, disabled by default) |
| AI Backend | Ollama (local/cloud, disabled by default) |

## Prerequisites

| Tool | Version | Installation |
|------|---------|--------------|
| VirtualBox | 7.0+ | [Download](https://www.virtualbox.org/wiki/Downloads) |
| Vagrant | 2.4+ | [Download](https://developer.hashicorp.com/vagrant/downloads) |
| kubectl | 1.37+ | [Install Guide](https://kubernetes.io/docs/tasks/tools/) |
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

### Cluster topology (Vagrant / VirtualBox)

```
┌────────────────────────────────────────────────────────────────────────────┐
│                              192.168.56.0/24                                 │
│                                                                              │
│  ┌──────────────────┐                  ┌──────────────────────────────────┐ │
│  │    Storage       │── NFS mount ────►│            ControlPlane          │ │
│  │  192.168.56.20   │◄─ VSO sync ──────│            192.168.56.10         │ │
│  │──────────────────│  (HTTPS :8200)   │──────────────────────────────────│ │
│  │  NFS Server      │◄─ DNS records ───│  kubeadm apiserver (OIDC auth)   │ │
│  │  Vault :8200     │  (etcd :2379)    │  Calico · MetalLB · Istio · Kiali│ │
│  │  CoreDNS :53     │                  │  cert-manager · Keycloak · VSO   │ │
│  │  etcd :2379      │                  │  ExternalDNS · KEDA · VPA        │ │
│  │  (systemd,       │                  │  Prometheus · Grafana · Loki     │ │
│  │   outside k8s)   │                  │  Tempo · Metrics Server          │ │
│  └────────┬─────────┘                  │                                  │ │
│           │                            └─────────────────┬────────────────┘ │
│           │ NFS mount                              kubeadm join             │
│           │                              ┌─────────────────┴──────────────┐ │
│           ├─────────────────────────────►│   Node01        │    Node02    │ │
│           └─────────────────────────────►│   .56.11        │    .56.12    │ │
│                                          │   Worker        │    Worker    │ │
│                                          │   Ollama*       │              │ │
│                                          └─────────────────┴──────────────┘ │
└────────────────────────────────────────────────────────────────────────────┘
  * Ollama only when Karpor AI is enabled. The components listed under
    ControlPlane are installed from there, but their pods are scheduled
    across all nodes.
```

Vault and the lab DNS (CoreDNS + etcd) run on the storage node **outside the
Kubernetes cluster** (systemd services), so secrets and DNS survive cluster rebuilds.
See [Lab DNS](#lab-dns-coredns--etcd--externaldns).

### Secrets & authentication flow

**Secrets** — Vault is the single source of truth; the Vault Secrets Operator
(VSO) bridges it into native Kubernetes Secrets:

```
Vault path (storage :8200, KV v2)        Vault Secrets Operator        K8s Secret (namespace/name)
secret/k8s-provisioner/…
  keycloak/admin            ──────────►  VaultStaticSecret  ───────►  keycloak/keycloak-admin
  keycloak/postgres         ──────────►  VaultStaticSecret  ───────►  keycloak/postgres-credentials
  grafana/admin             ──────────►  VaultStaticSecret  ───────►  monitoring/grafana-admin
  keycloak/clients/grafana  ──────────►  VaultStaticSecret  ───────►  monitoring/grafana-oidc
```

Each login lives at its own path with plain keys (`username`/`password`, or
`client_id`/`client_secret`), and each K8s Secret mirrors only its own path —
see [What Vault manages](#what-vault-manages).

**Authentication** — `kubectl` logs in against Keycloak; the apiserver validates
the token natively (no impersonation proxy), and group claims map to RBAC:

```
kubectl ──(1) oidc-login (PKCE, browser)──►  Keycloak  (realm: k8s)
   │                                            │  issuer https://keycloak.lab.internal/realms/k8s
   │ (2) id_token  (groups claim)               │
   ▼                                            ▼
kube-apiserver ──(3) validates via --authentication-config────────────────
   │   trusts the cert-manager lab CA; resolves keycloak.lab.internal via the lab DNS
   ▼
RBAC:  group oidc:k8s-admins → cluster-admin    ·    oidc:k8s-developers → view
```

Grafana reuses the same Keycloak realm for SSO (OAuth2). See
[Keycloak (Identity Provider / SSO)](#keycloak-identity-provider--sso) for the
full login walkthrough.

### Security posture (lab) & accepted risks

This is a **lab/learning cluster**, not a production environment. The threat model
assumes a trusted operator and a trusted local network; nothing is exposed to the
internet. The following are **deliberate, accepted trade-offs** for that context — they
are decisions, not oversights — and would need hardening before handling real user data:

- **No TLS on the Keycloak↔Postgres connection** and **no encryption at rest** for the
  Postgres data (it lives on a plaintext NFS export). Anyone with in-cluster network
  position or node/NFS filesystem access could read identity data. *Hardening path:* an
  Istio `PeerAuthentication` scoped to the Postgres workload (mTLS) and an encrypted
  StorageClass.
- **Vault uses a self-managed lab CA.** Vault serves HTTPS (`https://<storage>:8200`) with a
  certificate from its own CA, generated on the storage node; the CA private key stays
  on that node and there is no automated rotation (the server cert lasts 825 days).
  *Hardening path:* issue the Vault certificate from a managed PKI and rotate it.
- **Monitoring skips TLS verification in two places.** Prometheus scrapes the kubelets
  (cAdvisor and certificate metrics) and blackbox_exporter probes TLS endpoints without
  verifying the chain: kubeadm issues each kubelet a self-signed serving certificate, so
  there is no CA to verify against. Both still authenticate where it matters (Prometheus
  presents its ServiceAccount token; the probes only read expiry dates). The lab DNS etcd
  is not probed, since that would need its client certificate. *Hardening path:*
  `serverTLSBootstrap: true` in the kubelet config so kubelet certificates are signed by
  the cluster CA.
- **No database backups, audit logging, or retention/erasure policy**, and the Keycloak
  app uses the owning Postgres account (no least-privilege runtime role). Appropriate for
  ephemeral lab data; not for production.

Credentials are never hardcoded — they are randomly generated and stored in Vault, synced
to Kubernetes Secrets via VSO. The Postgres `Service` is `ClusterIP` (not exposed outside
the cluster).

## Project Structure

```
k8s-provisioner/
├── cmd/                       # CLI commands (Cobra)
│   ├── root.go                # Loads config.yaml, wires the executor
│   ├── provision.go           # provision common|controlplane|worker|workloads|all
│   ├── status.go              # Cluster status
│   ├── user.go                # User management (X.509 + RBAC)
│   ├── vault.go               # Vault token / status / get / set / unseal
│   └── vbox.go                # VirtualBox promiscuous mode
├── internal/
│   ├── config/                # config.yaml parser + validation
│   ├── executor/              # Shell executor (+ dry-run null object)
│   │   ├── executor.go
│   │   └── dryrun.go
│   ├── provisioner/           # Orchestration: InstallCommon → … → InstallWorkloads
│   │   ├── provisioner.go
│   │   └── hostprep.go        # swap, kernel modules, sysctl, DNS, CRI-O
│   ├── installer/             # One installer per component (manifests as Go strings)
│   │   ├── installer.go       # Installer interface + ordered workloadStep table
│   │   ├── timeouts.go        # Poll/timeout constants (no fixed sleeps)
│   │   ├── calico.go  istio.go  metallb.go  metrics.go  nfs_provisioner.go
│   │   ├── cert_manager.go    # Self-signed lab CA + one TLS certificate per Gateway
│   │   ├── external_dns.go    # Publishes Gateway hosts to the storage-node DNS (etcd)
│   │   ├── keycloak*.go       # OIDC IdP: deploy, realm, gateway, oidc (apiserver), grafana SSO
│   │   ├── vault.go  vault_client.go  vault_secrets_operator.go  secrets.go
│   │   ├── monitoring*.go     # Prometheus, Grafana, Alertmanager, exporters, storage, Istio scrape,
│   │   │                      #   kubelet/cAdvisor, DNS + TLS probes, the k8s-lab dashboard
│   │   ├── dashboards/        # k8s-lab dashboard sections (JSON, embedded in the binary)
│   │   ├── loki.go  tempo.go  kiali.go      # Logs, traces, mesh observability
│   │   ├── keda.go  vpa.go                  # Autoscaling
│   │   └── karpor.go  ollama.go             # Explorer + AI backend (opt-in)
│   ├── user/                  # User mgmt split: cert/rbac/kubeconfig/store
│   │   └── user.go  csr.go  kubeconfig.go  rbac.go  store.go
│   └── version/               # Build-time version injection
├── vagrant/
│   ├── Vagrantfile            # Builds the linux binary, boots 4 VMs, runs provision
│   ├── scripts/storage-dns.sh # Lab DNS on the storage node (etcd + CoreDNS, mTLS)
│   └── settings.yaml          # VM definitions (CPU/RAM/IP per node)
├── examples/                  # Sample apps & guides
│   ├── otel-demo-app/         # Go app with full trace → log correlation
│   ├── hello-app.yaml  podinfo-app.yaml  nfs-pv-pvc.yaml  vault-secret-app.yaml
│   ├── otel-operator-instrumentation.yaml
│   └── vault-usage.md  karpor-usage.md  monitoring-access.md
├── config.yaml                # Single source of truth (versions, CIDRs, toggles, nodes)
├── VERSION                    # Release version (read by auto-release CI)
├── main.go  go.mod  Makefile
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
# Copy the admin kubeconfig (it already points at https://192.168.56.10:6443)
vagrant ssh controlplane -c 'sudo cat /etc/kubernetes/admin.conf' > ~/.kube/config-lab

# Use the config
export KUBECONFIG=~/.kube/config-lab
kubectl get nodes
```

### 5. Host setup (DNS + trust the lab CAs)

Three one-time steps on **your machine** make every lab URL open over HTTPS without warnings:

1. **Resolve the lab domain** (`lab.internal`) through the DNS server on the storage node
   (`192.168.56.20`). ExternalDNS publishes every Istio Gateway host there, so there are
   no `/etc/hosts` edits.
2. **Trust two CAs**: the **lab CA** (cert-manager; signs Grafana, Keycloak, Kiali, …) and
   the **Vault CA** (storage node; signs `vault.lab.internal:8200`).
3. **macOS only:** allow apps other than Safari to reach the lab network.

When `vagrant up` finishes it exports both CAs to **`vagrant/certs/`** (gitignored) and
prints these exact commands with the CA fingerprints. Run them from the `vagrant/`
directory (the certificate paths are relative), **one command per line**. Each CA is
removed before it is trusted, so the same commands also replace the CAs of a previous lab:

**macOS**

```bash
sudo mkdir -p /etc/resolver && echo "nameserver 192.168.56.20" | sudo tee /etc/resolver/lab.internal
security find-certificate -a -Z -c "k8s-lab-ca" /Library/Keychains/System.keychain | awk '/^SHA-1/{print $NF}' | xargs -I{} sudo security delete-certificate -Z {} /Library/Keychains/System.keychain
sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain certs/k8s-lab-ca.crt
security find-certificate -a -Z -c "k8s-lab Vault CA" /Library/Keychains/System.keychain | awk '/^SHA-1/{print $NF}' | xargs -I{} sudo security delete-certificate -Z {} /Library/Keychains/System.keychain
sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain certs/k8s-lab-vault-ca.crt
```

`add-trusted-cert` alone does not replace a CA with the same name, it adds another copy.
Browsers cope, but Go clients such as kubelogin may pick a stale copy and fail with
`x509: ECDSA verification failure`.

Then open **System Settings → Privacy & Security → Local Network** and enable your browser
(Brave, Chrome, Firefox, …), terminal and IDE. Without it they fail with
`ERR_ADDRESS_UNREACHABLE` / `no route to host`, even though Safari works (Apple apps
are exempt). Fully quit (Cmd+Q) and reopen the browser.

**Linux** (Debian/Ubuntu with systemd-resolved; `vboxnet0` = the VirtualBox host-only interface)

```bash
sudo resolvectl dns vboxnet0 192.168.56.20 && sudo resolvectl domain vboxnet0 '~lab.internal'
sudo cp certs/*.crt /usr/local/share/ca-certificates/ && sudo update-ca-certificates
```

`resolvectl` settings last until the interface goes down; to persist them use a
NetworkManager/netplan DNS setting for the host-only interface. On Fedora/RHEL copy the
certificates to `/etc/pki/ca-trust/source/anchors/` and run `sudo update-ca-trust`.

**Windows** (admin PowerShell)

```powershell
Add-DnsClientNrptRule -Namespace ".lab.internal" -NameServers "192.168.56.20"
certutil -delstore Root "k8s-lab-ca"; certutil -addstore -f Root certs\k8s-lab-ca.crt
certutil -delstore Root "k8s-lab Vault CA"; certutil -addstore -f Root certs\k8s-lab-vault-ca.crt
```

**Check it:**

```bash
dscacheutil -q host -a name grafana.lab.internal   # macOS → ip_address: 192.168.56.200
getent hosts grafana.lab.internal                  # Linux
Resolve-DnsName grafana.lab.internal               # Windows
openssl x509 -in certs/k8s-lab-ca.crt -noout -fingerprint -sha256   # compare with the vagrant up output
```

> **Firefox** keeps its own certificate store: either import the `.crt` files under
> *Settings → Privacy & Security → Certificates → View Certificates → Authorities*, or set
> `security.enterprise_roots.enabled = true` in `about:config` (macOS/Windows) to use the
> system store.
>
> **Why trusting the CA is required, not optional:** Keycloak and Grafana send HSTS, so
> Chromium browsers (Brave, Chrome, Edge) refuse to "proceed anyway" on an untrusted
> certificate. Safari still lets you click through, which hides the problem.

#### After recreating the lab

`vagrant destroy` + `vagrant up` generates **new** CAs and a new cluster CA, so everything
that trusted the old ones stops working until you:

1. **Re-run the host setup commands** printed by `vagrant up` (above): they remove the old
   lab and Vault CAs and trust the new files in `vagrant/certs/`. On Linux, also drop
   certificates of a lab that is gone with
   `sudo rm /usr/local/share/ca-certificates/k8s-lab-*.crt && sudo update-ca-certificates --fresh`
   before copying the new ones.
2. **Fetch the kubeconfigs again**: both embed CAs. The admin one (step 4) holds the old
   cluster CA, and the OIDC one (step 7) holds the old lab CA in `--certificate-authority-data`,
   so kubelogin fails with `x509: certificate signed by unknown authority` even when the
   system trusts the new CA.

```bash
vagrant ssh controlplane -c 'sudo cat /etc/kubernetes/admin.conf' > ~/.kube/config-lab
vagrant ssh controlplane -c \
  'sudo k8s-provisioner vault get k8s-provisioner/kubeconfig-oidc config -c /etc/k8s-provisioner/config.yaml' \
  > ~/.kube/config-oidc
```

The DNS rule does not change and needs no action. Upgrading from a lab that used
`*.local` names? Comment out or remove those lines in `/etc/hosts`.

To verify the chain from the command line (expected `Verify return code: 0 (ok)`):

```bash
echo | openssl s_client -connect grafana.lab.internal:443 -servername grafana.lab.internal \
  -CAfile certs/k8s-lab-ca.crt 2>/dev/null | grep "Verify return code"
```

### 7. kubectl login via Keycloak (OIDC)

The cluster's API server trusts Keycloak as an OIDC provider. Group → role mapping:
`k8s-admins` → `cluster-admin`, `k8s-developers` → `view`. A ready-made kubeconfig
(CA-verified, no `insecure-skip-tls-verify`) is generated and stored in Vault.

```bash
# Install the kubelogin plugin (one-time)
brew install int128/kubelogin/kubelogin          # macOS
kubectl krew install oidc-login                   # or via krew

# Fetch the prepared OIDC kubeconfig from Vault (run from the vagrant/ directory)
vagrant ssh controlplane -c \
  'sudo k8s-provisioner vault get k8s-provisioner/kubeconfig-oidc config -c /etc/k8s-provisioner/config.yaml' \
  > ~/.kube/config-oidc

# Look up the test-user password (group k8s-admins → cluster-admin)
vagrant ssh controlplane -c \
  'sudo k8s-provisioner vault get k8s-provisioner/keycloak/users/k8sadmin password -c /etc/k8s-provisioner/config.yaml'

# Use it — a browser opens for Keycloak login (user: k8sadmin)
KUBECONFIG=~/.kube/config-oidc kubectl get nodes
```

> Requires step 5 (host setup) first, since kubelogin
> talks to `https://keycloak.lab.internal`.

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
k8s-provisioner vault status              # Vault connectivity and seal status
k8s-provisioner vault token               # Print the root token (reads vault-init.json; run on the controlplane)
k8s-provisioner vault get <path> [key]    # Read a secret (all keys, or a single key)
k8s-provisioner vault set <path> <key> <value>  # Write a key (existing keys are preserved)
k8s-provisioner vault unseal              # Unseal Vault after a storage node reboot
```

Example:
```bash
export VAULT_TOKEN=hvs.xxxx
./build/k8s-provisioner-darwin-arm64 vault get k8s-provisioner/grafana/admin
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
  timezone: "America/Sao_Paulo"   # IANA name for every node (incl. storage) and Grafana; empty = UTC

versions:
  kubernetes: "1.37"
  crio: "v1.37"
  calico: "3.33.0"
  metallb: "0.16.0"
  istio: "1.31.3"

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
  logging: "loki"                 # Options: loki, none (requires monitoring)
  tracing: "otel-tempo"           # Options: otel-tempo, none (requires monitoring; trace→log links need logging)
  karpor: "none"                  # Options: enabled, none (disabled by default — uses ~1.5 CPU, ~2GB RAM)
  keycloak: "enabled"             # Options: enabled, none (OIDC for kubectl + Grafana SSO)
  vpa: "enabled"                  # Options: enabled, none
  keda: "enabled"                 # Options: enabled, none
  external_dns: "enabled"         # Options: enabled, none (publishes Gateway hosts to the lab DNS)

# Lab DNS on the storage node (CoreDNS + etcd); see "Lab DNS"
dns:
  domain: "lab.internal"          # grafana.lab.internal, keycloak.lab.internal, ...

# HashiCorp Vault (runs on the storage node, outside the cluster)
vault:
  enabled: true
  addr: ""     # Empty = derived as https://<storage-node-ip>:8200 from nodes:. Set to override.
  token: ""    # env K8S_PROV_VAULT_TOKEN; empty = auto-resolve from vault-init.json
  ca_cert: ""  # env VAULT_CACERT; empty = /etc/k8s-provisioner/vault-ca.crt (fetched from the storage node)

# Karpor AI (requires karpor: "enabled" above)
karpor_ai:
  enabled: false
  backend: "ollama"
  model: "llama3.2:3b"

# Ollama (requires karpor_ai.enabled: true)
# API key for cloud models — if Vault is enabled, store it there and leave this empty
ollama:
  api_key: ""        # env OLLAMA_API_KEY
```

Secret fields can be supplied through environment variables instead of `config.yaml`: `K8S_PROV_VAULT_TOKEN`, `K8S_PROV_SSH_PASSWORD`, `KARPOR_AUTH_TOKEN` and `OLLAMA_API_KEY`.

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
  memory: "8192"    # Extra for AI workloads (Ollama, if enabled)
  cpus: "2"
  role: "worker"
- name: "node02"
  ip: "192.168.56.12"
  memory: "4096"
  cpus: "2"
  role: "worker"
```

## Lab DNS (CoreDNS + etcd + ExternalDNS)

Every lab hostname (`<name>.lab.internal`) is resolved by a DNS server on the storage
node, outside the cluster like Vault — so it survives cluster rebuilds:

```
Istio Gateway (hosts: grafana.lab.internal)
        │  watched by
        ▼
ExternalDNS (in-cluster) ──mTLS──► etcd :2379 (storage) ──► CoreDNS :53 (storage)
                                    /skydns/internal/lab/...        │
                                                                    ▼
                          host (/etc/resolver) · nodes (resolv.conf) · pods (CoreDNS stub zone)
```

- **CoreDNS** (`/etc/coredns/Corefile`) serves `lab.internal`: static records for the
  nodes (`storage`, `controlplane`, `node01`, …, plus `vault`) and every record ExternalDNS
  writes to etcd; everything else is forwarded to public resolvers.
- **etcd** only accepts clients with a certificate from its own CA (`/etc/etcd/tls`, created
  by `vagrant/scripts/storage-dns.sh`); the provisioner copies the ExternalDNS client cert
  into the `external-dns-etcd` Secret over SSH.
- **ExternalDNS** (`components.external_dns`) watches Istio Gateways and LoadBalancer
  Services and publishes their hosts with `policy=sync`: deleting a Gateway removes its record.
- **Nodes** use the storage node as their first nameserver, so the apiserver reaches the
  Keycloak issuer by name. **Pods** resolve the domain through a stub zone in the cluster
  CoreDNS that forwards to the storage node.

**Publishing your own app** — put the hostname in the Gateway and give the Gateway its own
certificate from the lab CA:

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: myapp-tls
  namespace: istio-system        # where the ingress gateway reads TLS secrets
spec:
  secretName: myapp-tls
  issuerRef: {name: lab-ca-issuer, kind: ClusterIssuer}
  dnsNames: ["myapp.lab.internal"]
---
apiVersion: networking.istio.io/v1
kind: Gateway
metadata:
  name: myapp
spec:
  selector:
    istio: ingressgateway
  servers:
  - port: {number: 443, name: https, protocol: HTTPS}
    tls: {mode: SIMPLE, credentialName: myapp-tls}
    hosts: ["myapp.lab.internal"]
```

> **One certificate per Gateway, never a shared/wildcard one.** Browsers reuse an HTTP/2
> connection for every host its certificate covers, but Istio routes by the SNI the
> connection was opened with — so with a shared certificate, opening Keycloak and then
> Grafana sends the Grafana request to Keycloak's Gateway and returns **404**. Separate
> Gateways also keep maintenance on one app from affecting the others.

Within ~30s `https://myapp.lab.internal` resolves everywhere. For a LoadBalancer Service,
annotate it with `external-dns.kubernetes.io/hostname: myapp.lab.internal`.

**Changing the domain** — set `dns.domain` in `config.yaml` before `vagrant up` (it ends up
in the TLS certificates, the Keycloak issuer and the Vault certificate, so an existing lab
must be recreated) and use the new name in the host resolver rule.

**Troubleshooting:** start with the **DNS — lab** row of the
[lab dashboard](#lab-dashboard-k8s-lab) (unpublished hosts, last sync, errors, failures),
then:

```bash
dig @192.168.56.20 grafana.lab.internal +short           # ask the lab DNS directly
kubectl -n external-dns logs deploy/external-dns          # "Add/set key /skydns/..." per record
vagrant ssh storage -c 'systemctl status etcd coredns --no-pager'
vagrant ssh storage -c 'sudo journalctl -u coredns -n 20 --no-pager'
vagrant ssh storage -c 'sudo etcdctl --endpoints=https://127.0.0.1:2379 \
  --cacert=/etc/etcd/tls/ca.crt --cert=/etc/etcd/tls/external-dns.crt \
  --key=/etc/etcd/tls/external-dns.key get /skydns --prefix --keys-only'
```

## HashiCorp Vault

Vault is installed automatically on the storage node (`https://192.168.56.20:8200`) as a systemd service, **outside the Kubernetes cluster**, so secrets survive cluster rebuilds. It serves **HTTPS** with a certificate from its own CA (see [Trusting the Vault CA](#trusting-the-vault-ca)). During `provision all`, the provisioner:

1. Initializes Vault (5 unseal keys, threshold 3) and unseals it automatically
2. Saves the init data to `/etc/vault.d/vault-init.json` on the storage node
3. Enables the **KV v2 secrets engine** at `secret/k8s-provisioner/`
4. Configures the **Kubernetes auth method** so pods can authenticate with their ServiceAccount
5. Generates a random **Grafana admin password** and stores it
6. Stores the Ollama and Karpor **API keys** (if configured)

### Retrieving Vault credentials

The init data is saved in two places during `provision all`:

| Location | Path | Access |
|----------|------|--------|
| Storage node | `/etc/vault.d/vault-init.json` | `vagrant ssh storage` |
| Controlplane (backup) | `/etc/k8s-provisioner/vault-init.json` | `vagrant ssh controlplane` |

**Option 1 — from the storage node (primary):**
```bash
vagrant ssh storage -c 'sudo cat /etc/vault.d/vault-init.json' | jq .
```

**Option 2 — from the controlplane (backup):**
```bash
vagrant ssh controlplane -c 'sudo cat /etc/k8s-provisioner/vault-init.json' | jq .
```

**Option 3 — root token only, with the project CLI:**
```bash
vagrant ssh controlplane -c 'sudo k8s-provisioner vault token'
```

The returned JSON contains:
```json
{
  "keys": ["unseal-key-1", "unseal-key-2", "..."],
  "root_token": "hvs.XXXXXXXXXXXXXXXX"
}
```

Use the `root_token` to access the UI or authenticate the CLI.

### Trusting the Vault CA

Vault's CA is generated on the storage node on the first `vagrant up`
(`/etc/vault.d/tls/ca.crt`) and kept across re-provisioning. It is separate from the
cluster's lab CA because Vault starts before the cluster (and cert-manager) exists.
`vagrant up` exports it as `vagrant/certs/k8s-lab-vault-ca.crt`; trust it together with
the lab CA in [Quick Start step 5](#5-host-setup-dns--trust-the-lab-cas).

If you prefer not to trust it system-wide, point the CLIs at the file instead:
`export VAULT_CACERT=$PWD/certs/k8s-lab-vault-ca.crt` (honored by both `vault` and
`k8s-provisioner`).

Inside the lab nothing is manual: the provisioner fetches the CA to
`/etc/k8s-provisioner/vault-ca.crt` on the controlplane, the Vault Secrets Operator gets
it as the `vault-ca` Secret, and on the storage node `VAULT_ADDR`/`VAULT_CACERT` are
preset for login shells (`/etc/profile.d/vault.sh`).

### Accessing the UI

```
https://192.168.56.20:8200/ui
```

Log in with the `root_token` obtained above.

> The UI's **Copy** / **Copy JSON** buttons use the browser clipboard API, which only
> works on HTTPS pages with a trusted certificate — trust the Vault CA first (above).

### Retrieving the Grafana password

```bash
# With the project CLI
./build/k8s-provisioner-darwin-arm64 vault get k8s-provisioner/grafana/admin password

# With the Vault CLI
export VAULT_ADDR=https://192.168.56.20:8200   # needs the Vault CA trusted, or VAULT_CACERT
export VAULT_TOKEN=$(vagrant ssh storage -c 'sudo cat /etc/vault.d/vault-init.json' | jq -r .root_token)
vault kv get -field=password secret/k8s-provisioner/grafana/admin
```

### Checking status

```bash
./build/k8s-provisioner-darwin-arm64 vault status
```

### Vault CLI operations

Set the environment variables before using the Vault CLI:

```bash
export VAULT_ADDR=https://192.168.56.20:8200   # needs the Vault CA trusted, or VAULT_CACERT
export VAULT_TOKEN=$(vagrant ssh storage -c 'sudo cat /etc/vault.d/vault-init.json' | jq -r .root_token)
```

#### Listing and reading secrets

```bash
# List all secret paths (folders end with /)
vault kv list secret/k8s-provisioner/
vault kv list secret/k8s-provisioner/keycloak/users

# Read a login (username + password)
vault kv get secret/k8s-provisioner/keycloak/users/k8sadmin

# Read a specific field
vault kv get -field=password secret/k8s-provisioner/grafana/admin
vault kv get -field=ollama_api_key secret/k8s-provisioner/api-keys
```

#### Creating and updating secrets

```bash
# Add a new secret (keeps the existing ones)
vault kv patch secret/k8s-provisioner/api-keys my_secret="value"

# Replace all secrets in a path
vault kv put secret/k8s-provisioner/api-keys \
  ollama_api_key="olka_xxxxx" \
  karpor_auth_token="xxxxx"

# Create a new path
vault kv put secret/my-app/config \
  db_password="strong-password" \
  api_key="api-key"
```

#### Versions and history

```bash
# Show the version history of a secret
vault kv metadata get secret/k8s-provisioner/api-keys

# Read a specific version
vault kv get -version=1 secret/k8s-provisioner/api-keys

# Restore a previous version
vault kv undelete -versions=1 secret/k8s-provisioner/api-keys
```

#### Managing policies

```bash
# List policies
vault policy list

# Show an existing policy
vault policy read k8s-provisioner

# Create a custom policy
vault policy write my-app - <<EOF
path "secret/data/my-app/*" {
  capabilities = ["read"]
}
path "secret/data/k8s-provisioner/api-keys" {
  capabilities = ["read"]
}
EOF
```

#### Kubernetes auth method

```bash
# Check the k8s auth configuration
vault auth list
vault read auth/kubernetes/config

# List configured roles
vault list auth/kubernetes/role

# Show role details
vault read auth/kubernetes/role/k8s-provisioner

# Create a role for a new application
vault write auth/kubernetes/role/my-app \
  bound_service_account_names="my-app-sa" \
  bound_service_account_namespaces="default" \
  policies="my-app" \
  ttl="1h"
```

#### Testing pod authentication

```bash
# Inside the pod — authenticate with the ServiceAccount token
SA_TOKEN=$(cat /var/run/secrets/kubernetes.io/serviceaccount/token)

vault write auth/kubernetes/login \
  role="k8s-provisioner" \
  jwt="$SA_TOKEN"
```

### Example app

```bash
# Deploy an app that fetches secrets from Vault via an init container
kubectl apply -f examples/vault-secret-app.yaml

# Follow authentication and secret fetching
kubectl logs -l app=vault-demo -c vault-fetch-secrets

# See the main container using the secret
kubectl logs -l app=vault-demo -c app

# Inspect the secret file (in memory)
kubectl exec -it deploy/vault-demo-app -- cat /vault/secrets/grafana-admin.json

# Clean up
kubectl delete -f examples/vault-secret-app.yaml
```

See [examples/vault-usage.md](examples/vault-usage.md) for the full guide.

### Secrets flow

```
config.yaml / env vars (first run only)
    └─► VaultInstaller stores them in Vault
              ├─► grafana/admin, keycloak/{admin,postgres,users/*,clients/grafana}
              │     (one path per login; passwords randomly generated)
              ├─► api-keys: ollama_api_key
              └─► api-keys: karpor_auth_token

              ↓ provisioner continues

    VSO ─────────► mirrors each login path into its K8s Secret
                       └─► grafana-admin, grafana-oidc, keycloak-admin, postgres-credentials
                               └─► pods use secretKeyRef

    Ollama ──────► reads ollama_api_key from Vault
                       └─► creates K8s Secret ollama-api-key
                               └─► Pod uses secretKeyRef

    Karpor ──────► reads karpor_auth_token from Vault
                       └─► passed via helm --set (no file on disk)
```

After the first deploy, secrets live **only in Vault**. Remove the keys from `config.yaml`:

```yaml
ollama:
  api_key: ""   # empty — Vault is now the source of truth
```

### Best practices

| Practice | Why |
|----------|-----|
| Never commit `config.yaml` with API keys filled in (use env vars instead) | Avoids accidental exposure in git |
| Use Vault as the single source of truth after the first deploy | Ensures traceability and centralized rotation |
| Create least-privilege policies per application | A compromised secret doesn't expose the others |
| Use `kv patch` to update without overwriting other fields | Preserves the KV v2 version history |
| Never use the root token in applications | Create scoped tokens with `vault token create -policy=my-app` |
| Store secrets only in `emptyDir.medium: Memory` volumes | Secrets never hit the VM disks |
| Rotate the root token after the initial setup | `vault token revoke <root_token>` after creating operational tokens |

### Rotating the Grafana password

```bash
# Save the new password in Vault; VSO updates the grafana-admin Secret within 30s
vault kv patch secret/k8s-provisioner/grafana/admin password="new-strong-password"

# Restart Grafana to apply it
kubectl rollout restart deployment/grafana -n monitoring
```

### Disabling Vault

```yaml
# config.yaml
vault:
  enabled: false
```

In this case there are no default passwords: the Grafana and Keycloak credentials are generated randomly and written to `/etc/k8s-provisioner/grafana-credentials.txt` and `/etc/k8s-provisioner/keycloak-credentials.txt` (mode `0600`) on the controlplane, and API keys come straight from `config.yaml` or the environment variables.

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
| kubelet / cAdvisor | Per-container CPU, memory, throttling, PSI and OOM metrics |
| blackbox_exporter | TLS certificate expiry of the apiserver, kubelets, Vault and every lab host |
| DNS targets | CoreDNS in the cluster, ExternalDNS, and CoreDNS on the storage node |

### Hostnames

All web UIs are exposed via the same Istio Ingress Gateway and resolved by the lab DNS
(see [Quick Start step 5](#5-host-setup-dns--trust-the-lab-cas) and [Lab DNS](#lab-dns-coredns--etcd--externaldns)).

---

### Accessing Grafana

Grafana is exposed via Istio Ingress Gateway:

```bash
open https://grafana.lab.internal
```

**Credentials:**
- Username: `admin`
- Password: generated randomly and stored in Vault — retrieve it with:
  ```bash
  vault kv get -field=password secret/k8s-provisioner/grafana/admin
  ```
  > If Vault is disabled: `sudo cat /etc/k8s-provisioner/grafana-credentials.txt` on the controlplane

### Accessing Prometheus

```bash
open https://prometheus.lab.internal
```

### Accessing Alertmanager

```bash
open https://alertmanager.lab.internal
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

### Lab dashboard (`k8s-lab`)

Provisioned with the cluster: **Dashboards → k8s-lab → k8s-lab**
(`https://grafana.lab.internal/d/k8s-lab/k8s-lab`). One screen, one collapsible row per
concern:

```
▼ Resources      CPU/memory usage vs request vs limit, throttling, PSI, OOM, restarts,
                 per workload revision (filters at the top apply here)
▼ SLOs           DNS objectives: SLI, error budget left, burn rate, alerts firing
▼ DNS — cluster  CoreDNS in kube-system: queries by zone, response codes, forward latency
▼ DNS — lab      ExternalDNS (records requested vs published, last sync, errors)
                 + CoreDNS on the storage node (queries, failures, latency, cache)
▼ Certificates   every certificate by days left: cert-manager, kubelet client certs,
                 and TLS probes of the apiserver, kubelets, Vault and each lab host
▶ DNS changes    ExternalDNS "A record added/removed" log lines (Loki; collapsed)
```

Pick a **Namespace** and **Workload** at the top for the Resources row; the other rows
cover the whole lab and ignore those filters. Rows only appear when their source exists
(no storage node → no lab DNS row; no Loki → no DNS change log).

| Row | Metrics from | Notes |
|---|---|---|
| Resources | kube-state-metrics + kubelet cAdvisor, through the `k8s-lab-resources` recording rules (`k8s_lab:*` series) | Panels and rules are pinned to an upstream commit in `versions.resource_dashboard`; bump it to update. PSI panels need kernel PSI (on in Debian 13). |
| SLOs | `k8s-lab-slos` recording rules (`slo:error_ratio:ratio_rate*`, `slo:objective:ratio`) | See [DNS SLOs](#dns-slos). |
| DNS — cluster | CoreDNS `:9153` in `kube-system` (job `kube-dns`) | The zone `lab.internal.` shows pod lookups forwarded to the storage node. |
| DNS — lab | ExternalDNS `:7979` (job `external-dns`), CoreDNS on storage `:9153` (job `lab-dns`) | **Unpublished hosts > 0** means a Gateway host is not resolvable yet. |
| Certificates | cert-manager, kubelet `/metrics`, blackbox_exporter TLS probes (job `blackbox`) | Probes read the certificate each endpoint presents, so a renewed-but-not-reloaded certificate shows up. **Lab CA expires in** is the CA your machine trusts. |

**Adding your own row:** drop a `{"panels": [...]}` file (a `row` panel first, `y` from 0)
in `internal/installer/dashboards/`, embed it in `monitoring_dashboard.go` and add it to
`labDashboardSections()`; `buildLabDashboard` stacks it below the others.

### DNS SLOs

Every lab URL depends on DNS, so the DNS path has service level objectives over a
**7-day window** (not the usual 28: the lab is rebuilt often):

| SLO | SLI (good events / time) | Objective |
|---|---|---|
| `coredns-cluster-availability` | Cluster DNS answers without SERVFAIL/REFUSED | 99.9% |
| `coredns-cluster-latency` | Cluster DNS answers within 128 ms | 99% |
| `coredns-lab-availability` | Lab DNS (storage node, lab zone) answers without SERVFAIL/REFUSED | 99.9% |
| `coredns-lab-latency` | Lab DNS answers within 128 ms (lab zone; upstream forwarding excluded) | 99% |
| `external-dns-freshness` | Share of time ExternalDNS synced within the last 2 minutes | 99% |
| `external-dns-coverage` | Share of time no requested host stays unpublished for over 2 minutes (one 30s sync is normal propagation) | 99% |

Rules live in the `k8s-lab-slos` PrometheusRule, generated from the SLO table in
`internal/installer/monitoring_slo.go`. Alerts use multi-window burn rates
([Google SRE Workbook, "Alerting on SLOs"](https://sre.google/workbook/alerting-on-slos/))
and reach Alertmanager:

| Alert | Fires when | Severity |
|---|---|---|
| `SLOErrorBudgetBurnFast` | error rate over 1h **and** 5m above 14.4× the sustainable rate (~9% of the 7-day budget in an hour) | critical |
| `SLOErrorBudgetBurnSlow` | error rate over 6h **and** 30m above 6× | warning |
| `DNSScrapeTargetDown` | `kube-dns`, `lab-dns` or `external-dns` not scraped for 5m | warning |

Notes for a low-traffic lab:
- CoreDNS burn alerts need at least 60 queries in the alert window, so one failed lookup
  out of three does not page. No traffic counts as no errors.
- A stopped CoreDNS reports no metrics, so its SLOs cannot see the outage;
  `DNSScrapeTargetDown` covers that. ExternalDNS SLOs are time-based and count the time
  it is down as bad (but not the minutes before its first scrape at install time).
- To change an objective or add an SLO, edit `dnsSLOs()`; the rules, alerts and the
  dashboard table follow.

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

> **Note:** The cert-manager ServiceMonitor is automatically created during provisioning, and
> the [lab dashboard](#lab-dashboard-k8s-lab) already shows every cert-manager certificate by
> days left. The lab CA is issued for 10 years (the per-host certificates keep cert-manager's
> 90 days and renew silently under it), so the CA your machine trusts does not change while
> the lab lives.

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

1. Open **https://grafana.lab.internal**
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

### API Server Audit Logs

The control plane is bootstrapped with **Kubernetes API server audit logging** enabled. The
provisioner runs `kubeadm init` from a generated `ClusterConfiguration`
(`internal/provisioner/provisioner.go`) that sets `--audit-policy-file` and
`--audit-log-path=-`. Writing to `-` (stdout) means audit events become part of the
`kube-apiserver` container's log stream, so the **existing Alloy collector ships them to Loki
via the Kubernetes API — the same path as every other pod log**. No file on disk, no
privileged log shipper.

**Audit policy** (`/etc/kubernetes/audit/policy.yaml`, first match wins):

| What | Level |
|------|-------|
| `get`/`list`/`watch`, health/version/metrics endpoints | `None` (dropped — high volume, low value) |
| `secrets`, `serviceaccounts`, RBAC roles/bindings (mutations) | `RequestResponse` (full body) |
| `pods/exec`, `pods/attach`, `pods/portforward` | `Request` |
| every other create/update/delete/patch | `Metadata` |

> Note: reads of secrets are *not* logged (matched by the `get/list/watch = None` rule first)
> to bound volume. Reorder the policy if secret reads must be audited.

**View in Grafana (Explore → Loki):**

```logql
# All API server audit events
{container="kube-apiserver"} | json | kind = "Event"

# Secret/RBAC mutations only
{container="kube-apiserver"} | json | objectRef_resource =~ "secrets|roles|rolebindings|clusterroles|clusterrolebindings"

# Who exec'd into a pod
{container="kube-apiserver"} | json | objectRef_subresource = "exec"
```

**View on the node (raw):**

```bash
kubectl logs -n kube-system kube-apiserver-controlplane | grep '"audit.k8s.io/v1"' | jq .
```

**Verify it works:**

```bash
kubectl create secret generic audit-test --from-literal=k=v
# then query Loki/Grafana — a RequestResponse event for 'create secrets' should appear
```

> Trade-off (lab): audit events are interleaved with the API server's operational logs (filter
> by `{container="kube-apiserver"}`), and there is no separate on-node audit file. In exchange,
> audit is shipped off-node to Loki with zero extra infrastructure. For stricter setups, switch
> `--audit-log-path` to a file with rotation and add a dedicated (privileged) log shipper.

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

1. Open **https://grafana.lab.internal**
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
# Access (no login required — anonymous mode)
open https://kiali.lab.internal/kiali
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
ISTIO_VERSION=release-1.31
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

> **Note:** Karpor is **disabled by default** because it needs extra resources (~1.5 CPU, ~2GB RAM). To enable it:
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
open https://karpor.lab.internal
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

> **Note:** Ollama is **disabled by default** and is only installed when Karpor AI is enabled:
> ```yaml
> karpor_ai:
>   enabled: true   # Enables Ollama automatically
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
  api_key: "olka_your_key_here"  # First deploy only — then keep it in Vault (or use env OLLAMA_API_KEY)
```

> If Vault is enabled, store the API key directly in Vault and leave `api_key: ""` in config.yaml:
> ```bash
> vault kv patch secret/k8s-provisioner/api-keys ollama_api_key="olka_your_key"
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

# Test (ExternalDNS publishes the Gateway host within ~30s)
curl http://podinfo.lab.internal
```

### Httpbin (Istio sample)

```bash
# Create namespace with Istio injection
kubectl create namespace demo
kubectl label namespace demo istio-injection=enabled

# Deploy httpbin
kubectl apply -n demo -f https://raw.githubusercontent.com/istio/istio/release-1.31/samples/httpbin/httpbin.yaml

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
    - "httpbin.lab.internal"
---
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: httpbin
  namespace: demo
spec:
  hosts:
  - "httpbin.lab.internal"
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

# Test (ExternalDNS publishes the Gateway host within ~30s)
curl http://httpbin.lab.internal/headers
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

Each login has its own path with plain keys, so the Vault UI shows a clean
login and password (and a K8s Secret gets only the login it needs):

| Secret path (`secret/…`) | Keys | Used by |
|---|---|---|
| `k8s-provisioner/grafana/admin` | `username`, `password` | Grafana local admin |
| `k8s-provisioner/keycloak/admin` | `username`, `password` | Keycloak admin console |
| `k8s-provisioner/keycloak/postgres` | `username`, `password` | PostgreSQL (Keycloak DB) |
| `k8s-provisioner/keycloak/users/k8sadmin` | `username`, `password` | Realm user `k8sadmin` (cluster-admin) |
| `k8s-provisioner/keycloak/users/developer` | `username`, `password` | Realm user `developer` (view) |
| `k8s-provisioner/keycloak/clients/grafana` | `client_id`, `client_secret` | Grafana OAuth2 client |
| `k8s-provisioner/api-keys` | `ollama_api_key`, `karpor_auth_token`, `alertmanager_config` | API keys and optional overrides |
| `k8s-provisioner/kubeconfig-oidc` | `config` | Ready-made OIDC kubeconfig |

> Upgrading from a version that kept every login as a prefixed key in `api-keys`
> (`keycloak_k8sadmin_password`, …)? The next provisioning moves each value to its
> path above, keeping the same passwords, and removes the old keys.

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
# vault.lab.internal is a static record in the lab DNS
open https://vault.lab.internal:8200

# Or use the IP directly
open https://192.168.56.20:8200
```

Login with the `root_token` from `vault-init.json`. The browser must trust the Vault CA (see [Trusting the Vault CA](#trusting-the-vault-ca)).

### Accessing Vault via CLI

```bash
export VAULT_ADDR=https://192.168.56.20:8200   # needs the Vault CA trusted, or VAULT_CACERT
export VAULT_TOKEN=hvs.XXXXXXXXXXXXXXXXXXXXXXXXXXXX   # from vault-init.json

# List secrets
vault kv list secret/k8s-provisioner

# Read a login
vault kv get secret/k8s-provisioner/keycloak/users/k8sadmin

# Update Grafana password
vault kv patch secret/k8s-provisioner/grafana/admin password=NewPassword123
```

> **Note:** After updating a password in Vault, restart the affected component (e.g. `kubectl rollout restart deployment/grafana -n monitoring`).

### Re-sealing and unsealing

Vault is automatically unsealed during provisioning. If the storage node is restarted, Vault will be sealed again. To unseal manually:

```bash
vagrant ssh storage

# Unseal with 3 of the 5 keys from vault-init.json
# (VAULT_ADDR and VAULT_CACERT are preset by /etc/profile.d/vault.sh)
vault operator unseal <key-1>
vault operator unseal <key-2>
vault operator unseal <key-3>
```

### config.yaml

```yaml
vault:
  addr: "https://192.168.56.20:8200"
  token: ""     # Leave empty — token is auto-read from /etc/k8s-provisioner/vault-init.json
  ca_cert: ""   # Leave empty — CA is fetched to /etc/k8s-provisioner/vault-ca.crt
```

---

## Keycloak (Identity Provider / SSO)

Keycloak provides OIDC authentication for `kubectl` and Single Sign-On (SSO) for Grafana. It runs inside the Kubernetes cluster with a PostgreSQL backend.

### Access

```bash
# Via Istio Gateway, TLS (resolved by the lab DNS — see Quick Start step 5)
open https://keycloak.lab.internal
```

The Keycloak `Service` is `ClusterIP` (not exposed outside the cluster); all access is
through the TLS gateway. Realm/client/user configuration is applied in-cluster via
`kubectl exec` (`kcadm.sh`), so no external port is needed.

### Admin credentials

```
URL:      https://keycloak.lab.internal  (TLS via Istio Gateway)
Username: admin
Password: (from Vault → secret/k8s-provisioner/keycloak/admin → password)
```

Every Keycloak login has its own Vault path under `secret/k8s-provisioner/keycloak/`:

| Vault path | Keys | Description |
|-----------|------|-------------|
| `keycloak/admin` | `username`, `password` | Admin console (`admin`) |
| `keycloak/postgres` | `username`, `password` | PostgreSQL (`keycloak`) |
| `keycloak/users/k8sadmin` | `username`, `password` | Realm user `k8sadmin` |
| `keycloak/users/developer` | `username`, `password` | Realm user `developer` |
| `keycloak/clients/grafana` | `client_id`, `client_secret` | Grafana OIDC client |

To customize passwords before provisioning:

```bash
export VAULT_ADDR=https://192.168.56.20:8200
export VAULT_CACERT=/etc/k8s-provisioner/vault-ca.crt
export VAULT_TOKEN=$(cat /etc/k8s-provisioner/vault-init.json | jq -r .root_token)

vault kv patch secret/k8s-provisioner/keycloak/users/k8sadmin password="MyStr0ng@Pass"
vault kv patch secret/k8s-provisioner/keycloak/users/developer password="An0ther@Pass123"
```

> **Note:** Run this after Vault is initialized but before Keycloak is installed (or recreate the cluster).

To retrieve via Vault UI: `https://vault.lab.internal:8200` → **secret** → `k8s-provisioner/keycloak/users/k8sadmin`

To retrieve via CLI:
```bash
export VAULT_ADDR=https://192.168.56.20:8200
export VAULT_CACERT=/etc/k8s-provisioner/vault-ca.crt
export VAULT_TOKEN=$(cat /etc/k8s-provisioner/vault-init.json | grep root_token | cut -d'"' -f4)
vault kv get secret/k8s-provisioner/keycloak/users/k8sadmin
```

### Pre-configured realm: `k8s`

| Resource | Details |
|---|---|
| Realm | `k8s` |
| kubectl client | `kubectl` (public, PKCE enabled) |
| Grafana client | `grafana` (confidential) |
| Admin group | `k8s-admins` → `cluster-admin` RBAC |
| Developer group | `k8s-developers` → `view` RBAC |
| Test user (admin) | `k8sadmin` / (Vault: `keycloak/users/k8sadmin`) |
| Test user (dev) | `developer` / (Vault: `keycloak/users/developer`) |

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

1. Open `https://keycloak.lab.internal/admin` → realm `k8s` → Users → Add user
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

# VAULT_ADDR / VAULT_CACERT are preset by /etc/profile.d/vault.sh
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

1. Open `https://192.168.56.20:8200` in your browser (with the Vault CA trusted) and log in with the root token
2. Navigate to **secret → k8s-provisioner → kubeconfig-oidc**
3. Copy the value of the `config` field
4. Paste into a new file and save as `~/.kube/kubeconfig-oidc`

**New user: first-time setup (once)**

```bash
# 1. Install kubelogin
brew install int128/kubelogin/kubelogin   # macOS

# 2. Resolve the lab domain via the storage node (required to reach the OIDC issuer)
sudo mkdir -p /etc/resolver
echo "nameserver 192.168.56.20" | sudo tee /etc/resolver/lab.internal

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
| `k8sadmin` | `vault kv get -field=password secret/k8s-provisioner/keycloak/users/k8sadmin` | Admin |
| `developer` | `vault kv get -field=password secret/k8s-provisioner/keycloak/users/developer` | Viewer |

- Users in `k8s-admins` group → Grafana `Admin` role
- All other users → Grafana `Viewer` role
- Local admin login still works: `admin` / (password from Vault)

### Registering a new application in Keycloak

To protect a new application with Keycloak SSO, create a client in the `k8s` realm.

**Via Admin Console** (`https://keycloak.lab.internal/admin`):

1. Login with `admin` credentials
2. Select realm **k8s** (top-left dropdown)
3. Go to **Clients** → **Create client**
4. Fill in:
   - **Client type**: `OpenID Connect`
   - **Client ID**: your app name (e.g., `myapp`)
5. Enable **Client authentication** if your app can keep a secret (confidential client)
6. Set **Valid redirect URIs**: `https://myapp.lab.internal/*`
7. Set **Web origins**: `https://myapp.lab.internal`
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
  -s "redirectUris=[\"https://myapp.lab.internal/*\"]" \
  -s enabled=true

# Public client with PKCE (SPAs / CLIs)
$KCADM create clients -r k8s \
  -s clientId=myapp-spa \
  -s publicClient=true \
  -s "redirectUris=[\"https://myapp.lab.internal/*\"]" \
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

### Browser can't open a lab URL

Match the error to the cause (setup steps in [Quick Start step 5](#5-host-setup-dns--trust-the-lab-cas)):

| Symptom | Cause | Fix |
|---|---|---|
| `no such host` / `ERR_NAME_NOT_RESOLVED` (also `kubectl` OIDC login: `lookup keycloak.lab.internal: no such host`) | The host does not send `lab.internal` to the lab DNS | Add the resolver rule. Check: `dig @192.168.56.20 grafana.lab.internal +short` (server) vs `dscacheutil -q host -a name grafana.lab.internal` (macOS) |
| Same, only in one browser | The browser's *Secure DNS* (DNS-over-HTTPS) bypasses the system resolver | Turn off *Use secure DNS* (Brave/Chrome: `…://settings/security`); check with `brave://net-internals/#dns` |
| `ERR_ADDRESS_UNREACHABLE`, `no route to host` (macOS), while Safari works | macOS Local Network privacy blocks the app | System Settings → Privacy & Security → Local Network → enable the app, then restart it |
| `NET::ERR_CERT_AUTHORITY_INVALID` with no "proceed" link | CA not trusted (HSTS forbids bypassing), or the lab was recreated and the CA changed | Trust the CAs from `vagrant/certs/`; after a rebuild remove the old ones first |
| **404** on one site right after visiting another | HTTP/2 connection reused across Gateways that share a certificate | Every Gateway needs its own certificate (see [Lab DNS](#lab-dns-coredns--etcd--externaldns)); clear stale connections with `brave://net-internals/#sockets` → *Flush socket pools*, or restart the browser |

Quick checks from the host:

```bash
dig @192.168.56.20 keycloak.lab.internal +short      # lab DNS answers → 192.168.56.200
curl -sI https://grafana.lab.internal | head -1      # DNS + TLS trust + routing in one go
```

### Grafana shows "No data" / an empty Data source

| Symptom | Cause | Fix |
|---|---|---|
| Every Prometheus/Tempo panel empty, **Data source** dropdown blank, Grafana log `Could not find plugin definition for data source` | Grafana tried to auto-update its bundled plugins on the read-only root filesystem and lost them (`Failed to install plugin ... read-only file system`) | The installer sets `GF_PLUGINS_PREINSTALL_AUTO_UPDATE=false`; on an older install: `kubectl set env deploy/grafana -n monitoring GF_PLUGINS_PREINSTALL_AUTO_UPDATE=false` |
| k8s-lab Resources row empty for a workload | It needs a few minutes of cAdvisor history; check `up{job="kubelet"}` in Prometheus | Wait, or pick a workload that has been running longer |
| A Certificates or DNS row stat says "No data" / "down" | The scrape target is down | Prometheus → **Status → Targets**, jobs `blackbox`, `kube-dns`, `external-dns`, `lab-dns` |

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
| Node01 | 8 GB | 2 | 20 GB | Worker + AI Workloads (optional) |
| Node02 | 4 GB | 2 | 20 GB | Worker |
| **Total** | **20 GB** | **9** | **70 GB** | |

> **Karpor + Ollama are disabled by default.** To enable them, Node01 needs the full 8GB to load the llama3.2:3b model (~4GB RAM).

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