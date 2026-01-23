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
- **Password:** admin123

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
