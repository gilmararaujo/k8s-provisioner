package installer

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/techiescamp/k8s-provisioner/internal/config"
)

// Audit alerts: LogQL rules the Loki ruler evaluates every minute over the API
// server audit events (see the audit policy in the provisioner) and sends to
// Alertmanager. Each looks back 5 minutes, so an alert stays active for about 5
// minutes after the last matching request.
//
// Two filters keep the provisioner and the cluster's own machinery out:
//
//   - auditActor drops the node, control plane and kube-system identities
//     (and the Calico operator), whose exec, RBAC and privileged-pod requests
//     are how the cluster runs. Any other service account is kept: a stolen
//     token is the usual way in.
//   - The control-plane source filter drops requests sent from a control plane
//     node, where the provisioner runs kubectl as the admin credential while it
//     installs the lab. Something running there unattended is out of view, a
//     deliberate lab trade-off.
const (
	auditSelector = `{namespace="kube-system", container="kube-apiserver"} |= "audit.k8s.io/v1"`
	auditActor    = `user_username !~ "system:(node:|kube-|apiserver|serviceaccount:kube-system:|serviceaccount:tigera-operator:).*"`
	// auditFields extracts only the fields the rules read: a bare "| json" would
	// turn every nested field of RequestResponse bodies into a label.
	auditFields = `json verb="verb", user_username="user.username", objectRef_resource="objectRef.resource", ` +
		`objectRef_subresource="objectRef.subresource", objectRef_namespace="objectRef.namespace", ` +
		`objectRef_name="objectRef.name", responseStatus_code="responseStatus.code"`
	auditWindow = "5m"
	// kialiPortForward drops Kiali reading istiod's debug endpoints, a
	// port-forward every few seconds its Role allows only in istio-system.
	kialiPortForward = `user_username != "system:serviceaccount:istio-system:kiali" or ` +
		`objectRef_subresource != "portforward" or objectRef_namespace != "istio-system"`
	// privilegedSpec matches a privileged pod spec. Two filters joined by "or",
	// not one alternation: Loki simplifies `"(a|b)":true|"c"` into a match on
	// any of the words, which every Istio-injected pod (`"privileged":false`)
	// satisfies.
	privilegedSpec = "|~ `\"(privileged|hostPID|hostNetwork|hostIPC)\":true` or `\"hostPath\":\\{`"
)

// auditRulesConfigMap is mounted into Loki as its "fake" tenant's rule file
// (the tenant Loki uses when authentication is off).
const auditRulesConfigMap = "loki-rules"

// notFromControlPlane is a line filter dropping requests whose first source IP
// is a control plane node.
func notFromControlPlane(cfg *config.Config) string {
	var ips []string
	for _, n := range cfg.Nodes {
		if n.Role == "controlplane" && n.IP != "" {
			ips = append(ips, regexp.QuoteMeta(n.IP))
		}
	}
	if len(ips) == 0 {
		return ""
	}
	return fmt.Sprintf(" !~ `\"sourceIPs\":\\[\"(%s)\"`", strings.Join(ips, "|"))
}

// auditCount wraps a pipeline in the per-window count every audit alert uses.
func auditCount(by, pipeline string) string {
	return fmt.Sprintf("sum by (%s) (count_over_time(%s [%s]))", by, pipeline, auditWindow)
}

// auditAlertRules renders the Loki rule file.
func auditAlertRules(cfg *config.Config) (string, error) {
	notCP := notFromControlPlane(cfg)
	alert := func(name, severity, expr, summary, description string) promRule {
		return promRule{
			Alert: name, Expr: expr,
			Labels:      map[string]string{"severity": severity, "source": "audit"},
			Annotations: map[string]string{"summary": summary, "description": description},
		}
	}
	rules := []promRule{
		alert("AuditPodExec", "info",
			auditCount("user, target_namespace, pod", auditSelector+" |~ `\"subresource\":\"(exec|attach|portforward)\"`"+notCP+
				` | `+auditFields+` | objectRef_subresource=~"exec|attach|portforward" | `+auditActor+` | `+kialiPortForward+
				` | label_format user="{{.user_username}}", target_namespace="{{.objectRef_namespace}}", pod="{{.objectRef_name}}"`)+" > 0",
			"{{ $labels.user }} opened a shell or tunnel into {{ $labels.target_namespace }}/{{ $labels.pod }}",
			"exec, attach or port-forward into a pod. Expected when you debug; otherwise someone is running commands inside a workload."),
		alert("AuditClusterAdminGranted", "critical",
			auditCount("user, binding", auditSelector+" |= `\"name\":\"cluster-admin\"`"+notCP+
				` | `+auditFields+` | objectRef_resource=~"clusterrolebindings|rolebindings" | verb=~"create|update|patch" | responseStatus_code=~"2.." | `+auditActor+
				` | label_format user="{{.user_username}}", binding="{{.objectRef_name}}"`)+" > 0",
			"{{ $labels.user }} granted cluster-admin through {{ $labels.binding }}",
			"A role binding to cluster-admin was created or changed. Full control of the cluster is the usual goal of a privilege escalation."),
		alert("AuditAdminCredentialUsed", "info",
			auditCount("user, source", auditSelector+" |~ `\"username\":\"kubernetes-(super-)?admin\"`"+notCP+
				" | regexp `\"sourceIPs\":\\[\"(?P<source>[^\"]+)\"`"+
				` | `+auditFields+` | label_format user="{{.user_username}}"`)+" > 0",
			"The admin credential (admin.conf) was used from {{ $labels.source }}",
			"admin.conf is the break-glass credential: it bypasses Keycloak and RBAC (cluster-admin). Day-to-day access should use OIDC."),
		alert("AuditForbiddenBurst", "warning",
			auditCount("user", auditSelector+" |= `\"code\":403`"+
				` | `+auditFields+` | responseStatus_code="403" | `+auditActor+` | label_format user="{{.user_username}}"`)+" > 10",
			"{{ $labels.user }} was denied more than 10 requests in 5 minutes",
			"Repeated 403s usually mean an identity probing what it is allowed to do, e.g. a stolen service account token."),
		alert("AuditSecretsRead", "info",
			auditCount("user, target_namespace", auditSelector+" |= `\"resource\":\"secrets\"`"+notCP+
				` | `+auditFields+` | objectRef_resource="secrets" | verb=~"get|list|watch" | responseStatus_code=~"2.." | `+auditActor+
				` | label_format user="{{.user_username}}", target_namespace="{{.objectRef_namespace}}"`)+" > 0",
			"{{ $labels.user }} read secrets in {{ $labels.target_namespace }}",
			"A person read Secret values (an empty namespace means all namespaces). Workloads read secrets through their service accounts, which are not logged."),
		alert("AuditPrivilegedWorkload", "warning",
			auditCount("user, kind, target_namespace, name", auditSelector+" "+privilegedSpec+notCP+
				` | `+auditFields+` | verb=~"create|update|patch" | objectRef_resource=~"pods|deployments|daemonsets|statefulsets|replicasets|jobs|cronjobs" | responseStatus_code=~"2.." | `+auditActor+
				` | label_format user="{{.user_username}}", kind="{{.objectRef_resource}}", target_namespace="{{.objectRef_namespace}}", name="{{.objectRef_name}}"`)+" > 0",
			"{{ $labels.user }} created or changed a privileged {{ $labels.kind }} {{ $labels.target_namespace }}/{{ $labels.name }}",
			"privileged, hostPID, hostNetwork, hostIPC or a hostPath volume: each gives a pod access to its node."),
	}
	out, err := yaml.Marshal(map[string]any{"groups": []promRuleGroup{{Name: "k8s-lab-audit", Interval: "1m", Rules: rules}}})
	return string(out), err
}

// installAuditAlerts publishes the audit rules to the ConfigMap Loki reads them from.
func (l *Loki) installAuditAlerts() error {
	rules, err := auditAlertRules(l.config)
	if err != nil {
		return err
	}
	cm, err := yaml.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]string{"name": auditRulesConfigMap, "namespace": "monitoring"},
		"data":       map[string]string{"audit.yaml": rules},
	})
	if err != nil {
		return err
	}
	if _, err := l.exec.RunShellWithStdin("kubectl apply -f -", string(cm)); err != nil {
		return fmt.Errorf("apply audit alert rules: %w", err)
	}
	return nil
}
