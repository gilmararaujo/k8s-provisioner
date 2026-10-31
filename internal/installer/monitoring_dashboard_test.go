package installer

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const upstreamDashboard = `{"uid":"learnk8s-resource-review","title":"Kubernetes Resource Review","id":7,
 "links":[{"title":"guide","type":"link"}],
 "panels":[{"id":9,"type":"stat","title":"Current Pods","gridPos":{"h":3,"w":3,"x":0,"y":0},
            "links":[{"url":"https://learnkube.com/x"}],
            "fieldConfig":{"defaults":{},"overrides":[{"matcher":{"id":"byName","options":"Pod"},
              "properties":[{"id":"links","value":[{"url":"/d/6581e46e4e5c7ba40a07646395ef7b23"}]},{"id":"unit","value":"none"}]}]}},
           {"id":51,"type":"table","title":"Review","gridPos":{"h":11,"w":24,"x":0,"y":50}}]}`

func TestBuildLabDashboard_WrapsUpstreamInResourcesRow(t *testing.T) {
	out, err := buildLabDashboard([]byte(upstreamDashboard), "UTC")
	require.NoError(t, err)

	var d struct {
		UID    string `json:"uid"`
		Title  string `json:"title"`
		ID     *int   `json:"id"`
		Links  []map[string]any
		Panels []struct {
			Type    string         `json:"type"`
			Title   string         `json:"title"`
			GridPos map[string]int `json:"gridPos"`
		} `json:"panels"`
	}
	require.NoError(t, json.Unmarshal(out, &d))

	assert.Equal(t, "k8s-lab", d.UID)
	assert.Equal(t, "k8s-lab", d.Title)
	assert.Nil(t, d.ID, "a provisioned dashboard must not carry a database id")
	require.Len(t, d.Panels, 3)
	assert.Equal(t, "row", d.Panels[0].Type)
	assert.Equal(t, 1, d.Panels[1].GridPos["y"], "upstream panels move below the row")
	assert.Equal(t, 51, d.Panels[2].GridPos["y"])
	require.NotEmpty(t, d.Links)
	for _, l := range d.Links {
		assert.Contains(t, l["url"], "https://kubernetes.io/", "only kubernetes.io links at the top")
	}
}

func TestBuildLabDashboard_RejectsEmpty(t *testing.T) {
	_, err := buildLabDashboard([]byte(`{"panels":[]}`), "UTC")
	assert.ErrorContains(t, err, "no panels")
}

func TestBuildLabDashboard_StacksSectionsBelowResources(t *testing.T) {
	out, err := buildLabDashboard([]byte(upstreamDashboard), "UTC", slosSection, clusterDNSSection, labDNSSection, certificatesSection, labDNSLogsSection)
	require.NoError(t, err)

	var d struct {
		Panels []struct {
			ID      int            `json:"id"`
			Type    string         `json:"type"`
			Title   string         `json:"title"`
			GridPos map[string]int `json:"gridPos"`
			Panels  []struct {
				GridPos map[string]int `json:"gridPos"`
			} `json:"panels"`
		} `json:"panels"`
	}
	require.NoError(t, json.Unmarshal(out, &d))

	ids := map[int]bool{}
	var rows []int
	for i, p := range d.Panels {
		assert.False(t, ids[p.ID], "panel id %d duplicated", p.ID)
		ids[p.ID] = true
		if p.Type == "row" {
			rows = append(rows, i)
		}
	}
	require.Len(t, rows, 6, "Resources, SLOs, cluster DNS, lab DNS, Certificates, DNS logs")
	dnsRow, logsRow := d.Panels[rows[1]], d.Panels[rows[5]]
	assert.Equal(t, 62, dnsRow.GridPos["y"], "DNS starts right below the last Resources panel (y 51 + h 11)")
	assert.Greater(t, logsRow.GridPos["y"], dnsRow.GridPos["y"])
	require.NotEmpty(t, logsRow.Panels, "a collapsed row carries its panels")
	assert.Equal(t, logsRow.GridPos["y"]+1, logsRow.Panels[0].GridPos["y"], "nested panels move with their row")
}

func TestLabDashboardSections_FollowConfig(t *testing.T) {
	cfg := externalDNSConfig()
	m := NewMonitoring(cfg, &fakeShell{})
	assert.Equal(t, [][]byte{slosSection, clusterDNSSection, labDNSSection, certificatesSection}, m.labDashboardSections(), "no Loki, no change log")

	cfg.Components.Logging = "loki"
	assert.Equal(t, [][]byte{slosSection, clusterDNSSection, labDNSSection, certificatesSection, labDNSLogsSection}, m.labDashboardSections(),
		"the collapsed change log goes last")

	cfg.Nodes = nil // no storage node, no lab DNS
	assert.Equal(t, [][]byte{slosSection, clusterDNSSection, certificatesSection}, m.labDashboardSections())
}

func TestLabDNSSection_SelectsLabDNSJobs(t *testing.T) {
	for _, job := range []string{externalDNSJob, labDNSJob} {
		assert.Contains(t, string(labDNSSection), `job=\"`+job+`\"`, "panels must select the scrape job created by installDNSMonitoring")
	}
	assert.Contains(t, string(clusterDNSSection), `job=\"`+clusterDNSJob+`\"`)
}

func TestBuildLabDashboard_DropsLinksToMissingDashboards(t *testing.T) {
	out, err := buildLabDashboard([]byte(upstreamDashboard), "UTC")
	require.NoError(t, err)

	assert.NotContains(t, string(out), "learnkube.com")
	assert.NotContains(t, string(out), "/d/6581e46e4e5c7ba40a07646395ef7b23", "kubernetes-mixin dashboards are not installed")
	assert.Contains(t, string(out), `"id": "unit"`, "other field overrides are kept")
	assert.NotContains(t, string(out), "resource-review", "no upstream project names on screen")
}

func TestBuildLabDashboard_UsesTheLabTimezone(t *testing.T) {
	out, err := buildLabDashboard([]byte(upstreamDashboard), "America/Sao_Paulo")
	require.NoError(t, err)
	assert.Contains(t, string(out), `"timezone": "America/Sao_Paulo"`, "not the viewer's browser")
}

func TestAddImageColumn_ShowsImageNextToContainer(t *testing.T) {
	panel := map[string]any{
		"title":       reviewTable,
		"targets":     []any{map[string]any{"refId": "AB"}},
		"fieldConfig": map[string]any{"overrides": []any{}},
		"transformations": []any{map[string]any{"id": "organize", "options": map[string]any{
			"excludeByName": map[string]any{},
			"renameByName":  map[string]any{},
			"indexByName":   map[string]any{"revision": 0.0, "container": 1.0, "Value #AB": 2.0},
		}}},
	}
	addImageColumn(panel)

	targets := panel["targets"].([]any)
	require.Len(t, targets, 2)
	assert.Contains(t, targets[1].(map[string]any)["expr"], "workload_revision_pod_container_info")
	opts := panel["transformations"].([]any)[0].(map[string]any)["options"].(map[string]any)
	assert.Equal(t, 2, int(opts["indexByName"].(map[string]any)["image"].(int)))
	assert.Equal(t, 3.0, opts["indexByName"].(map[string]any)["Value #AB"], "Image population moves right")
	assert.Equal(t, "Image", opts["renameByName"].(map[string]any)["image"])
	assert.Equal(t, true, opts["excludeByName"].(map[string]any)["Value #IMG"])
	assert.Equal(t, true, opts["excludeByName"].(map[string]any)["Value #AB"], "the digest count is hidden")

	overrides := panel["fieldConfig"].(map[string]any)["overrides"].([]any)
	require.Len(t, overrides, 2)
	img := overrides[0].(map[string]any)
	assert.Equal(t, "Image", img["matcher"].(map[string]any)["options"])
	assert.Contains(t, img["properties"], map[string]any{"id": "custom.width", "value": 340}, "wide enough for a full image name")
}

func TestAddImageColumn_IgnoresOtherPanels(t *testing.T) {
	panel := map[string]any{"title": "Something else", "targets": []any{}}
	addImageColumn(panel)
	assert.Empty(t, panel["targets"])
}

func TestBuildLabDashboard_UsesTheLabSeriesPrefix(t *testing.T) {
	upstream := `{"panels":[{"id":9,"type":"stat","title":"x","gridPos":{"h":3,"w":3,"x":0,"y":0},
	  "targets":[{"expr":"sum(resource_review:container_cpu_usage_cores:rate5m)"}]}]}`
	out, err := buildLabDashboard([]byte(upstream), "UTC")
	require.NoError(t, err)

	assert.Contains(t, string(out), "k8s_lab:container_cpu_usage_cores:rate5m")
	assert.NotContains(t, string(out), "resource_review", "must match the renamed recording rules")
}
