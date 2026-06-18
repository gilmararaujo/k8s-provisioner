package installer

// Cross-referenced network endpoints. Named so values that must stay in sync
// across files live in one place (NM-5). Ports embedded once inside a single
// YAML/shell template are intentionally left inline.
const (
	apiServerPort = 6443 // kube-apiserver HTTPS port

	// kubeloginListenAddr is the local OIDC callback the kubelogin plugin binds
	// to. It must match the redirect URIs registered for the kubectl client in
	// Keycloak (see keycloak_realm.go).
	kubeloginListenAddr = "127.0.0.1:8000"
)
