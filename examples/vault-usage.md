# HashiCorp Vault — Guia de Uso

O Vault roda no storage node (`192.168.56.20:8200`) fora do cluster Kubernetes.

## Acessar a UI

```
http://192.168.56.20:8200/ui
```

Token de acesso:
```bash
vagrant ssh storage -c 'sudo cat /etc/vault.d/vault-init.json'
```

---

## Verificar status pelo CLI

```bash
./build/k8s-provisioner-darwin-arm64 vault status
```

---

## App de exemplo — vault-secret-app.yaml

Demonstra o padrão de injeção de secrets via init container.

### Deploy

```bash
kubectl apply -f examples/vault-secret-app.yaml
```

### Verificar logs do init container (busca o secret)

```bash
kubectl logs -l app=vault-demo -c vault-fetch-secrets
```

### Verificar o container principal (usa o secret)

```bash
kubectl logs -l app=vault-demo -c app
```

### Inspecionar o arquivo de secret dentro do pod

```bash
kubectl exec -it deploy/vault-demo-app -- cat /vault/secrets/api-keys.json
```

### Remover

```bash
kubectl delete -f examples/vault-secret-app.yaml
```

---

## Como funciona (fluxo)

```
Pod inicia
  └─> init container: vault-fetch-secrets
        1. Lê o token do ServiceAccount em /var/run/secrets/kubernetes.io/serviceaccount/token
        2. POST /v1/auth/kubernetes/login  →  obtém VAULT_TOKEN
        3. GET  /v1/secret/data/k8s-provisioner/api-keys  →  secrets JSON
        4. Escreve em /vault/secrets/api-keys.json (volume em memória)
  └─> container principal: app
        - Lê /vault/secrets/api-keys.json (somente leitura)
        - Secrets nunca passam por variáveis de ambiente nem ficam em disco
```

---

## Adicionar novos secrets

```bash
# Da sua máquina (com VAULT_TOKEN)
export VAULT_ADDR=http://192.168.56.20:8200
export VAULT_TOKEN=<root_token>

vault kv put secret/k8s-provisioner/api-keys \
  ollama_api_key="sua-chave" \
  meu_novo_secret="valor"
```

Ou pelo CLI do projeto:
```bash
./build/k8s-provisioner-darwin-arm64 vault get-secret k8s-provisioner/api-keys
```

---

## Usar em seus próprios deployments

Copie o padrão do `vault-secret-app.yaml`:

1. Adicione `serviceAccountName: vault-demo` (ou crie seu próprio SA)
2. Adicione o init container `vault-fetch-secrets`
3. Monte o volume `vault-secrets` como `emptyDir.medium: Memory`
4. No container principal, leia de `/vault/secrets/`