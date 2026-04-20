# Monitoring Stack Access Guide

## Components Installed

| Component | Description | Namespace |
|-----------|-------------|-----------|
| Prometheus Operator | Manages Prometheus instances | monitoring |
| Prometheus | Metrics collection and storage | monitoring |
| Grafana | Visualization and dashboards | monitoring |
| Node Exporter | Host metrics (CPU, memory, disk) | monitoring |
| kube-state-metrics | Kubernetes object metrics | monitoring |

## Accessing Grafana

Grafana is exposed via Istio Ingress Gateway.

### Via Istio Ingress (recommended)

```bash
# Get Istio Ingress IP
INGRESS_IP=$(kubectl get svc -n istio-system istio-ingressgateway -o jsonpath='{.status.loadBalancer.ingress[0].ip}')

# Add to /etc/hosts
echo "$INGRESS_IP grafana.local" | sudo tee -a /etc/hosts

# Access
open http://grafana.local
```

### Via Port Forward (alternative)

```bash
kubectl port-forward -n monitoring svc/grafana 3000:3000
# Access: http://localhost:3000
```

## Grafana Credentials

- **Username:** admin
- **Password:** gerada automaticamente e armazenada no Vault

Para recuperar a senha:

```bash
# Via CLI do projeto (na máquina host)
export VAULT_ADDR=http://192.168.56.20:8200
export VAULT_TOKEN=$(vagrant ssh storage -c 'sudo cat /etc/vault.d/vault-init.json' | jq -r .root_token)
vault kv get -field=grafana_admin_password secret/k8s-provisioner/api-keys

# Ou diretamente via CLI do projeto
./build/k8s-provisioner-darwin-arm64 vault get-secret k8s-provisioner/api-keys
```

> Se o Vault não estiver habilitado (`vault.enabled: false` no config.yaml), a senha padrão é `admin123`.

## Accessing Prometheus

```bash
kubectl port-forward -n monitoring svc/prometheus 9090:9090
# Access: http://localhost:9090
```

## Importing Dashboards

Grafana comes with Prometheus pre-configured as datasource. To add useful dashboards:

1. Go to Grafana → Dashboards → Import
2. Use these dashboard IDs from grafana.com:

| Dashboard | ID | Description |
|-----------|-----|-------------|
| Node Exporter Full | 1860 | Detailed host metrics |
| Kubernetes Cluster | 6417 | Cluster overview |
| Kubernetes Pods | 6336 | Pod metrics |
| Kubernetes Deployments | 8588 | Deployment status |

## Useful PromQL Queries

### CPU Usage by Node
```promql
100 - (avg by(instance) (rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)
```

### Memory Usage by Node
```promql
(1 - (node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)) * 100
```

### Pod CPU Usage
```promql
sum(rate(container_cpu_usage_seconds_total{namespace!=""}[5m])) by (namespace, pod)
```

### Pod Memory Usage
```promql
sum(container_memory_working_set_bytes{namespace!=""}) by (namespace, pod)
```

## Verifying Installation

```bash
# Check all monitoring pods
kubectl get pods -n monitoring

# Expected output:
# grafana-xxx                     Running
# kube-state-metrics-xxx          Running
# node-exporter-xxx (DaemonSet)   Running
# prometheus-operator-xxx         Running
# prometheus-prometheus-0         Running

# Check services
kubectl get svc -n monitoring
```

## Troubleshooting

### Prometheus not scraping targets

```bash
# Check ServiceMonitors
kubectl get servicemonitors -n monitoring

# Check Prometheus targets
kubectl port-forward -n monitoring svc/prometheus 9090:9090
# Then access http://localhost:9090/targets
```

### Grafana datasource not working

```bash
# Verify Prometheus service
kubectl get svc -n monitoring prometheus

# Test connectivity from Grafana pod
kubectl exec -n monitoring deploy/grafana -- wget -qO- http://prometheus:9090/api/v1/status/config
```
