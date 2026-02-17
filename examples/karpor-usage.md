# Karpor Usage Guide

## Overview

Karpor is a Kubernetes Explorer that provides intelligent search and insight capabilities for your cluster resources. It offers AI-powered analysis (optional) and a web-based interface for exploring your Kubernetes environment.

## Components Installed

| Component | Description | Namespace |
|-----------|-------------|-----------|
| Karpor Server | Main API server and web UI | karpor |
| Karpor Syncer | Synchronizes cluster resources | karpor |
| Karpor ETL | Processes and indexes data | karpor |

## Accessing Karpor

### Via Istio Ingress (recommended)

```bash
# Get Istio Ingress IP
INGRESS_IP=$(kubectl get svc -n istio-system istio-ingressgateway -o jsonpath='{.status.loadBalancer.ingress[0].ip}')

# Add to /etc/hosts
echo "$INGRESS_IP karpor.local" | sudo tee -a /etc/hosts

# Access
open http://karpor.local
```

### Via Port Forward (alternative)

```bash
kubectl port-forward -n karpor svc/karpor-server 7443:7443
# Access: http://localhost:7443
```

## Configuration

### Basic Configuration (config.yaml)

```yaml
components:
  karpor: "enabled"  # Options: enabled, none

versions:
  karpor: "0.6.1"
```

### AI Features Configuration

To enable AI-powered insights, configure the `karpor_ai` section in config.yaml:

```yaml
karpor_ai:
  enabled: true
  backend: "openai"     # Options: openai, deepseek, huggingface, azureopenai
  auth_token: "sk-..."  # Your API token
  base_url: ""          # Optional: custom endpoint URL
  model: ""             # Optional: specific model name
```

#### Supported AI Backends

| Backend | Description |
|---------|-------------|
| openai | OpenAI API (GPT-4, GPT-3.5, etc.) |
| azureopenai | Azure OpenAI Service |
| huggingface | Hugging Face models |
| ollama | Local models via Ollama (Llama, Mistral, CodeLlama, etc.) |

#### Using Ollama with Local Models

To use local models (runs inside the cluster):

```yaml
karpor_ai:
  enabled: true
  backend: "ollama"
  auth_token: ""           # Not needed for local models
  base_url: ""             # Uses internal Ollama service
  model: "llama3.2:1b"     # Local model (~1.3GB)

# No API key needed for local models
ollama:
  api_key: ""
```

**Available local models:**
- `llama3.2:1b` - Small, fast (~1.3GB RAM)
- `llama3.2:3b` - Medium (~4GB RAM)
- `qwen2.5-coder:7b` - Good for code (~8GB RAM)
- `llama3.1:8b` - Excellent quality (~10GB RAM)

#### Using Ollama with Cloud Models (Recommended)

Cloud models offer better performance without needing GPU resources:

1. Create an account at https://ollama.com/signup
2. Generate an API key at https://ollama.com/settings/keys
3. Configure in config.yaml:

```yaml
karpor_ai:
  enabled: true
  backend: "ollama"
  auth_token: ""                  # Not used (API key is in ollama section)
  base_url: ""                    # Uses internal Ollama service
  model: "minimax-m2.5:cloud"     # Cloud model

# Required for cloud models
ollama:
  api_key: "olka_your_api_key_here"
```

**Available cloud models:**
| Model | Description |
|-------|-------------|
| `minimax-m2.5:cloud` | Top performer, comparable to Claude Opus |
| `minimax-m2.1:cloud` | Previous version, very capable |
| `qwen3-coder:480b-cloud` | Excellent for code analysis |
| `glm-4.7:cloud` | Good general purpose |

**Advantages of cloud models:**
- No GPU required in the cluster
- Lower memory usage (~256MB vs ~4GB)
- Access to larger, more capable models
- Free tier available with daily limits

## Features

### Resource Search

Karpor provides powerful search capabilities:

```
# Search for deployments
kind:Deployment

# Search in specific namespace
namespace:monitoring kind:Pod

# Search by label
label:app=nginx

# Search by name pattern
name:*api*
```

### AI-Powered Analysis (if enabled)

When AI is enabled, you can:
- Get natural language explanations of resources
- Analyze potential issues and misconfigurations
- Get recommendations for resource optimization

### Cluster Insights

- View resource relationships and dependencies
- Analyze resource utilization patterns
- Track configuration drift

## Verifying Installation

```bash
# Check all Karpor pods
kubectl get pods -n karpor

# Expected output:
# karpor-server-xxx     Running
# karpor-syncer-xxx     Running
# karpor-etl-xxx        Running

# Check services
kubectl get svc -n karpor

# Check Istio Gateway and VirtualService
kubectl get gateway,virtualservice -n karpor
```

## Troubleshooting

### Karpor not accessible via Istio

```bash
# Verify Gateway is created
kubectl get gateway -n karpor

# Verify VirtualService
kubectl get virtualservice -n karpor

# Check Istio Ingress Gateway logs
kubectl logs -n istio-system -l app=istio-ingressgateway
```

### Karpor pods not starting

```bash
# Check pod events
kubectl describe pods -n karpor

# Check pod logs
kubectl logs -n karpor -l app.kubernetes.io/name=karpor-server
```

### AI features not working

1. Verify AI configuration in config.yaml
2. Check that auth_token is valid
3. Check Karpor server logs for AI-related errors:

```bash
kubectl logs -n karpor -l app.kubernetes.io/name=karpor-server | grep -i ai
```

## Uninstalling Karpor

```bash
# Remove Helm release
helm uninstall karpor -n karpor

# Remove namespace
kubectl delete namespace karpor
```

## References

- [Karpor Documentation](https://kusionstack.io/karpor/)
- [Karpor GitHub](https://github.com/KusionStack/karpor)
