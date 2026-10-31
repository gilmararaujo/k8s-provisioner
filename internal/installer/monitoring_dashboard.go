package installer

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
)

// Lab-owned dashboard sections: {"panels": [...]} laid out from y=0, each
// starting with its row panel. buildLabDashboard stacks them below Resources.
var (
	//go:embed dashboards/slos.json
	slosSection []byte
	//go:embed dashboards/cluster-dns.json
	clusterDNSSection []byte
	//go:embed dashboards/lab-dns.json
	labDNSSection []byte
	//go:embed dashboards/lab-dns-logs.json
	labDNSLogsSection []byte
	//go:embed dashboards/certificates.json
	certificatesSection []byte
)

// The lab dashboard: one screen, one collapsible row per concern. The Resources
// row is github.com/learnk8s/resource-review-mixin's dashboard as published at
// the pinned commit (Apache-2.0), so bumping versions.resource_dashboard updates it.
const (
	labDashboardUID       = "k8s-lab"
	labDashboardConfigMap = "grafana-dashboards"
)

// installLabDashboard renders the lab dashboard and publishes it to the
// ConfigMap Grafana's file provider reads (folder k8s-lab).
func (m *Monitoring) installLabDashboard() error {
	url, err := m.resourceReviewURL("generated/resource-review.json")
	if err != nil {
		return err
	}
	upstream, err := m.exec.RunShell("curl -fsSL --connect-timeout 10 --max-time 120 " + url)
	if err != nil {
		return fmt.Errorf("download resource-review dashboard: %w", err)
	}
	dashboard, err := buildLabDashboard([]byte(upstream), m.config.Timezone(), m.labDashboardSections()...)
	if err != nil {
		return err
	}

	cm, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]any{"name": labDashboardConfigMap, "namespace": "monitoring"},
		"data":       map[string]string{labDashboardUID + ".json": string(dashboard)},
	})
	if err != nil {
		return err
	}
	// Server-side apply: the dashboard is larger than the 256KiB annotation that
	// client-side apply would store as last-applied-configuration.
	if _, err := m.exec.RunShellWithStdin(
		"kubectl apply --server-side --force-conflicts --field-manager=k8s-provisioner -f -", string(cm)); err != nil {
		return fmt.Errorf("apply %s ConfigMap: %w", labDashboardConfigMap, err)
	}
	return nil
}

// labDashboardSections returns the lab sections that have data to show in
// this cluster, in screen order: DNS SLOs, cluster DNS, the lab DNS when it
// exists, certificates, and the (collapsed) DNS change log last, only with Loki.
func (m *Monitoring) labDashboardSections() [][]byte {
	sections := [][]byte{slosSection, clusterDNSSection}
	labDNS := m.config.StorageIP() != ""
	if labDNS {
		sections = append(sections, labDNSSection)
	}
	sections = append(sections, certificatesSection)
	if labDNS && m.config.ExternalDNSEnabled() && m.config.Components.Logging == "loki" {
		sections = append(sections, labDNSLogsSection)
	}
	return sections
}

// buildLabDashboard wraps the upstream resource-review panels in a "Resources"
// row, stacks the lab sections below it and gives the result the lab's identity.
func buildLabDashboard(resourceReview []byte, timezone string, sections ...[]byte) ([]byte, error) {
	resourceReview = bytes.ReplaceAll(resourceReview, []byte(upstreamSeriesPrefix), []byte(labSeriesPrefix))
	var d map[string]any
	if err := json.Unmarshal(resourceReview, &d); err != nil {
		return nil, fmt.Errorf("parse resource-review dashboard: %w", err)
	}
	upstream, _ := d["panels"].([]any)
	if len(upstream) == 0 {
		return nil, fmt.Errorf("resource-review dashboard has no panels")
	}

	panels := []any{dashboardRow(1, "Resources — CPU and memory by workload", 0)}
	for _, p := range upstream {
		shiftPanel(p, 1)
		panels = append(panels, p)
	}
	for _, section := range sections {
		var sec struct {
			Panels []any `json:"panels"`
		}
		if err := json.Unmarshal(section, &sec); err != nil {
			return nil, fmt.Errorf("parse lab dashboard section: %w", err)
		}
		top := bottom(panels)
		for _, p := range sec.Panels {
			shiftPanel(p, top)
			panels = append(panels, p)
		}
	}

	d["panels"] = panels
	d["uid"] = labDashboardUID
	d["title"] = "k8s-lab"
	d["tags"] = []string{"k8s-lab", "kubernetes", "resources", "dns", "certificates"}
	d["description"] = "Lab overview: workload resources, DNS SLOs, cluster and lab DNS, certificate expiry."
	// The lab is rebuilt often; a week-long default range is mostly empty.
	d["time"] = map[string]string{"from": "now-6h", "to": "now"}
	// The lab's timezone, not the viewer's browser (upstream: "browser").
	d["timezone"] = timezone
	delete(d, "id")
	d["links"] = kubernetesDocsLinks()
	for _, p := range panels {
		stripPanelLinks(p)
		addImageColumn(p)
	}
	return json.MarshalIndent(d, "", "  ")
}

// kubernetesDocsLinks are the dashboard's top links: the kubernetes.io pages
// behind each section (upstream links to third-party guides are dropped).
func kubernetesDocsLinks() []any {
	link := func(title, url string) map[string]any {
		return map[string]any{"title": title, "url": url, "type": "link", "icon": "doc", "targetBlank": true}
	}
	return []any{
		link("Resource management for Pods and containers", "https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/"),
		link("DNS for Services and Pods", "https://kubernetes.io/docs/concepts/services-networking/dns-pod-service/"),
		link("PKI certificates and requirements", "https://kubernetes.io/docs/setup/best-practices/certificates/"),
		link("Certificate management with kubeadm", "https://kubernetes.io/docs/tasks/administer-cluster/kubeadm/kubeadm-certs/"),
	}
}

// stripPanelLinks removes panel and table-cell links: upstream points them at
// kubernetes-mixin dashboards this lab does not install, so they would 404.
func stripPanelLinks(p any) {
	panel, _ := p.(map[string]any)
	delete(panel, "links")
	if fc, ok := panel["fieldConfig"].(map[string]any); ok {
		if defaults, ok := fc["defaults"].(map[string]any); ok {
			delete(defaults, "links")
		}
		overrides, _ := fc["overrides"].([]any)
		for _, o := range overrides {
			override, _ := o.(map[string]any)
			props, _ := override["properties"].([]any)
			kept := props[:0]
			for _, prop := range props {
				if pm, _ := prop.(map[string]any); pm["id"] != "links" {
					kept = append(kept, prop)
				}
			}
			override["properties"] = kept
		}
	}
	nested, _ := panel["panels"].([]any)
	for _, n := range nested {
		stripPanelLinks(n)
	}
}

// reviewTable is the upstream table whose "Image population" column only
// counts image digests ("1 known image", "Unknown images").
const reviewTable = "Workload revision and container review"

// imageNameQuery returns each revision's image names, with the same filters as
// the table's other queries.
const imageNameQuery = `max by (cluster, namespace, workload_type, workload, revision, revision_source, container, image) ` +
	`(max_over_time(` + labSeriesPrefix + `workload_revision_pod_container_info{namespace=~"$namespace", workload_type=~"$workload_type", ` +
	`workload=~"$workload", revision=~"$revision", pod=~"$pod", container=~"$container"}[$__range]))`

// addImageColumn replaces the review table's "Image population" count with an
// "Image" column (e.g. quay.io/calico/node:v3.33.0) next to Container; a
// revision running several images shows one row per image. A no-op if upstream
// reshapes the table.
func addImageColumn(p any) {
	panel, _ := p.(map[string]any)
	if panel["title"] != reviewTable {
		return
	}
	transforms, _ := panel["transformations"].([]any)
	var organize map[string]any
	for _, t := range transforms {
		if tm, _ := t.(map[string]any); tm["id"] == "organize" {
			organize, _ = tm["options"].(map[string]any)
		}
	}
	index, _ := organize["indexByName"].(map[string]any)
	exclude, _ := organize["excludeByName"].(map[string]any)
	rename, _ := organize["renameByName"].(map[string]any)
	targets, _ := panel["targets"].([]any)
	if index == nil || exclude == nil || rename == nil || len(targets) == 0 {
		return
	}

	panel["targets"] = append(targets, map[string]any{
		"datasource": map[string]string{"type": "prometheus", "uid": "${datasource}"},
		"expr":       imageNameQuery, "format": "table", "instant": true, "range": false, "refId": "IMG",
	})
	exclude["Value #IMG"] = true // the value is always 1; only the image label matters
	exclude["Value #AB"] = true  // "Image population": a digest count the Image column supersedes
	for k, v := range index {
		if pos, ok := v.(float64); ok && pos >= 2 {
			index[k] = pos + 1
		}
	}
	index["image"] = 2 // Revision, Container, Image, ...
	rename["image"] = "Image"

	// Image names and pod-UID revisions are long: size the Image column for a
	// full name and let both be inspected (click) instead of silently truncated.
	if fc, ok := panel["fieldConfig"].(map[string]any); ok {
		overrides, _ := fc["overrides"].([]any)
		fc["overrides"] = append(overrides,
			fieldOverride("Image", map[string]any{"custom.width": 340, "custom.inspect": true}),
			fieldOverride("Revision", map[string]any{"custom.inspect": true}))
	}
}

// fieldOverride returns a table override for the column named name.
func fieldOverride(name string, props map[string]any) map[string]any {
	properties := []any{}
	for _, id := range []string{"custom.width", "custom.inspect"} {
		if v, ok := props[id]; ok {
			properties = append(properties, map[string]any{"id": id, "value": v})
		}
	}
	return map[string]any{"matcher": map[string]any{"id": "byName", "options": name}, "properties": properties}
}

// dashboardRow returns an expanded row panel at y; the panels after it in the
// list (until the next row) belong to it.
func dashboardRow(id int, title string, y int) map[string]any {
	return map[string]any{
		"id": id, "type": "row", "title": title, "collapsed": false, "panels": []any{},
		"gridPos": map[string]int{"h": 1, "w": 24, "x": 0, "y": y},
	}
}

// shiftPanel moves a panel down by dy grid units, including the panels a
// collapsed row carries inside it.
func shiftPanel(p any, dy int) {
	panel, _ := p.(map[string]any)
	if pos, ok := panel["gridPos"].(map[string]any); ok {
		if y, ok := pos["y"].(float64); ok {
			pos["y"] = y + float64(dy)
		}
	}
	nested, _ := panel["panels"].([]any)
	for _, n := range nested {
		shiftPanel(n, dy)
	}
}

// bottom returns the first free grid row below panels.
func bottom(panels []any) int {
	maxY := 0
	for _, p := range panels {
		panel, _ := p.(map[string]any)
		pos, _ := panel["gridPos"].(map[string]any)
		y, _ := pos["y"].(float64)
		h, _ := pos["h"].(float64)
		if end := int(y + h); end > maxY {
			maxY = end
		}
	}
	return maxY
}
