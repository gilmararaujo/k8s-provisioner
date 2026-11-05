package provisioner

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/techiescamp/k8s-provisioner/internal/config"
)

// kubeletServingTimeout bounds the wait for every node's kubelet serving CSR.
// Kubelets request their serving certificate as soon as they start, so by the
// time a node is Ready its CSR is normally already pending.
const kubeletServingTimeout = 3 * time.Minute

const kubeletServingSigner = "kubernetes.io/kubelet-serving"

// csrList is the subset of `kubectl get csr -o json` the approver reads.
type csrList struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Spec struct {
			SignerName string   `json:"signerName"`
			Username   string   `json:"username"`
			Groups     []string `json:"groups"`
			Usages     []string `json:"usages"`
			Request    string   `json:"request"`
		} `json:"spec"`
		Status struct {
			Conditions []struct {
				Type string `json:"type"`
			} `json:"conditions"`
		} `json:"status"`
	} `json:"items"`
}

// kubeletNodes returns the nodes that run a kubelet, keyed by name.
func kubeletNodes(cfg *config.Config) map[string]config.NodeConfig {
	nodes := map[string]config.NodeConfig{}
	for _, n := range cfg.Nodes {
		if n.Role == "controlplane" || n.Role == "worker" {
			nodes[n.Name] = n
		}
	}
	return nodes
}

// validateKubeletServingCSR checks that a kubelet-serving CSR asks only for what
// its node should have: requested by that node's own credentials, for that
// node's name and the IP config.yaml gives it. kube-controller-manager signs
// approved requests with the cluster CA, so approving anything else would let a
// node present a certificate for another node, or for an arbitrary address.
func validateKubeletServingCSR(username string, groups, usages []string, request string, nodes map[string]config.NodeConfig) (string, error) {
	name, isNode := strings.CutPrefix(username, "system:node:")
	if !isNode || name == "" {
		return "", fmt.Errorf("requested by %q, not a node", username)
	}
	node, ok := nodes[name]
	if !ok {
		return "", fmt.Errorf("node %q is not in config.yaml", name)
	}
	if !slices.Contains(groups, "system:nodes") {
		return "", fmt.Errorf("requester %q is not in group system:nodes", username)
	}
	for _, u := range usages {
		if u != "digital signature" && u != "key encipherment" && u != "server auth" {
			return "", fmt.Errorf("usage %q is not allowed for a serving certificate", u)
		}
	}

	der, err := base64.StdEncoding.DecodeString(request)
	if err != nil {
		return "", fmt.Errorf("decode request: %w", err)
	}
	block, _ := pem.Decode(der)
	if block == nil {
		return "", fmt.Errorf("request is not PEM")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parse request: %w", err)
	}
	if csr.Subject.CommonName != username {
		return "", fmt.Errorf("subject CN %q does not match requester %q", csr.Subject.CommonName, username)
	}
	if len(csr.EmailAddresses) > 0 || len(csr.URIs) > 0 {
		return "", fmt.Errorf("email or URI SANs are not allowed")
	}
	for _, dns := range csr.DNSNames {
		if dns != name {
			return "", fmt.Errorf("DNS SAN %q is not the node name %q", dns, name)
		}
	}
	if len(csr.IPAddresses) == 0 {
		return "", fmt.Errorf("no IP SAN; the apiserver reaches kubelets by IP")
	}
	for _, ip := range csr.IPAddresses {
		if ip.String() != node.IP {
			return "", fmt.Errorf("IP SAN %s is not %s's address %s", ip, name, node.IP)
		}
	}
	return name, nil
}

// kubeletServingBootstrapEnabled reports whether kubeadm's shared kubelet config
// has kubelets request cluster-CA serving certificates. Clusters created before
// the kubeadm config set serverTLSBootstrap never send serving CSRs, so waiting
// for them would only time out.
func (p *Provisioner) kubeletServingBootstrapEnabled() bool {
	out, err := p.exec.RunShell("kubectl -n kube-system get configmap kubelet-config -o jsonpath='{.data.kubelet}'")
	return err == nil && strings.Contains(out, "serverTLSBootstrap: true")
}

// approveKubeletServingCSRs waits until each named node has an approved
// kubelet serving certificate, approving the pending requests that pass
// validateKubeletServingCSR. The apiserver verifies kubelets against the
// cluster CA (--kubelet-certificate-authority), so until a node's CSR is
// approved `kubectl logs/exec` and metrics scraping on that node fail.
// Requests that fail validation are reported and left pending.
func (p *Provisioner) approveKubeletServingCSRs(names []string, timeout time.Duration) error {
	if p.dryRun {
		fmt.Printf("[dry-run] would approve kubelet serving certificates for %v\n", names)
		return nil
	}

	r := csrReview{nodes: kubeletNodes(p.config), covered: map[string]bool{}, rejected: map[string]bool{}}
	deadline := time.Now().Add(timeout)
	for {
		if out, err := p.exec.RunShell("kubectl get csr -o json"); err == nil {
			if err := p.reviewKubeletServingCSRs(out, &r); err != nil {
				return err
			}
		}

		var missing []string
		for _, n := range names {
			if !r.covered[n] {
				missing = append(missing, n)
			}
		}
		if len(missing) == 0 {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("timeout waiting for kubelet serving certificates of %v", missing)
		}
		time.Sleep(defaultPollInterval)
	}
}

// csrReview carries approval state across polls: which nodes already have an
// approved serving certificate, and which CSRs were already reported invalid.
type csrReview struct {
	nodes    map[string]config.NodeConfig
	covered  map[string]bool
	rejected map[string]bool
}

// reviewKubeletServingCSRs approves the valid pending kubelet serving CSRs in
// one `kubectl get csr -o json` listing and records the covered nodes.
func (p *Provisioner) reviewKubeletServingCSRs(listing string, r *csrReview) error {
	var list csrList
	if err := json.Unmarshal([]byte(listing), &list); err != nil {
		return fmt.Errorf("parse CSR list: %w", err)
	}
	for _, item := range list.Items {
		if item.Spec.SignerName != kubeletServingSigner {
			continue
		}
		status := ""
		for _, c := range item.Status.Conditions {
			status = c.Type
		}
		if status == "Denied" || status == "Failed" {
			continue
		}

		name, err := validateKubeletServingCSR(item.Spec.Username, item.Spec.Groups, item.Spec.Usages, item.Spec.Request, r.nodes)
		switch {
		case err != nil:
			if status == "" && !r.rejected[item.Metadata.Name] {
				r.rejected[item.Metadata.Name] = true
				fmt.Printf("Warning: not approving kubelet serving CSR %s: %v\n", item.Metadata.Name, err)
			}
		case status == "Approved":
			r.covered[name] = true
		default:
			if _, err := p.exec.RunShell("kubectl certificate approve " + item.Metadata.Name); err != nil {
				return fmt.Errorf("approve kubelet serving CSR %s: %w", item.Metadata.Name, err)
			}
			fmt.Printf("Approved kubelet serving certificate for %s (%s)\n", name, item.Metadata.Name)
			r.covered[name] = true
		}
	}
	return nil
}
