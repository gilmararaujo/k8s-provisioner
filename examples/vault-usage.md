# HashiCorp Vault — Usage Guide

Vault runs on the storage node (`https://192.168.56.20:8200`), outside the Kubernetes cluster, and serves TLS with its own CA. Trust that CA first — see "Trusting the Vault CA" in the README.

## Accessing the UI

```
https://192.168.56.20:8200/ui
```

Access token:
```bash
vagrant ssh storage -c 'sudo cat /etc/vault.d/vault-init.json'
```

---

## Checking status with the CLI

```bash
./build/k8s-provisioner-darwin-arm64 vault status
```

---

## Example app — vault-secret-app.yaml

Demonstrates the secret-injection pattern using an init container.

### Deploy

```bash
kubectl apply -f examples/vault-secret-app.yaml
```

### Check the init container logs (fetches the secret)

```bash
kubectl logs -l app=vault-demo -c vault-fetch-secrets
```

### Check the main container (uses the secret)

```bash
kubectl logs -l app=vault-demo -c app
```

### Inspect the secret file inside the pod

```bash
kubectl exec -it deploy/vault-demo-app -- cat /vault/secrets/grafana-admin.json
```

### Clean up

```bash
kubectl delete -f examples/vault-secret-app.yaml
```

---

## How it works (flow)

```
Pod starts
  └─> init container: vault-fetch-secrets
        1. Reads the ServiceAccount token from /var/run/secrets/kubernetes.io/serviceaccount/token
        2. POST /v1/auth/kubernetes/login  →  gets VAULT_TOKEN
        3. GET  /v1/secret/data/k8s-provisioner/grafana/admin  →  login JSON
        4. Writes to /vault/secrets/grafana-admin.json (in-memory volume)
  └─> main container: app
        - Reads /vault/secrets/grafana-admin.json (read-only)
        - Secrets never go through environment variables and never touch disk
```

---

## Adding new secrets

```bash
# From your machine (with VAULT_TOKEN)
export VAULT_ADDR=https://192.168.56.20:8200
export VAULT_CACERT=vagrant/certs/k8s-lab-vault-ca.crt   # exported by vagrant up; or trust it system-wide (README "Host setup")
export VAULT_TOKEN=<root_token>

vault kv patch secret/k8s-provisioner/api-keys \
  ollama_api_key="your-key" \
  my_new_secret="value"
```

Read them back with the project CLI:
```bash
./build/k8s-provisioner-darwin-arm64 vault get k8s-provisioner/api-keys
```

---

## Using it in your own deployments

Copy the pattern from `vault-secret-app.yaml`:

1. Add `serviceAccountName: vault-demo` (or create your own SA)
2. Add the `vault-fetch-secrets` init container
3. Mount the `vault-secrets` volume as `emptyDir.medium: Memory`
4. In the main container, read from `/vault/secrets/`
