package installer

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// storageNodeJob is the Prometheus job for node_exporter on the storage node,
// which backs every PersistentVolume over NFS. The Storage dashboard section
// and the storage alerts select on it. node_exporter is installed on the
// storage node by vagrant/scripts/storage-node-exporter.sh.
const storageNodeJob = "storage-node"

// installStorageNodeMonitoring scrapes node_exporter on the storage node and
// adds the storage alerts. The storage node runs outside the cluster, so it is
// a static ScrapeConfig target (like the lab DNS) rather than a ServiceMonitor.
// The per-PVC and per-PV alerts read metrics already collected from inside the
// cluster (kubelet volume stats, kube-state-metrics).
func (m *Monitoring) installStorageNodeMonitoring() error {
	ip := m.config.StorageIP()
	if ip == "" {
		return nil // no storage node: nothing backs the PVs to monitor
	}
	scrape := fmt.Sprintf(`apiVersion: monitoring.coreos.com/v1alpha1
kind: ScrapeConfig
metadata:
  name: %[1]s
  namespace: monitoring
spec:
  jobName: %[1]s
  scrapeInterval: 30s
  staticConfigs:
  - targets: ["%[2]s:9100"]
    labels:
      node: storage`, storageNodeJob, ip)
	if _, err := m.exec.RunShellWithStdin("kubectl apply -f -", scrape); err != nil {
		return fmt.Errorf("apply storage node scrape target: %w", err)
	}
	rules, err := storageRules()
	if err != nil {
		return err
	}
	if _, err := m.exec.RunShellWithStdin("kubectl apply -f -", rules); err != nil {
		return fmt.Errorf("apply storage alert rules: %w", err)
	}
	return nil
}

// storageRules are the storage alerts, shown in Alertmanager and Grafana (no
// notification receiver is configured by default). They cover the storage node
// itself (reachability, free space on the export) and the cluster's view of
// storage (PVCs filling up, PVs not Bound).
func storageRules() (string, error) {
	// The export (/exports/k8s-volumes) lives on the storage node's root
	// filesystem, so its free space is the root mount's. rootfs is excluded
	// because node_exporter double-reports it on some kernels.
	root := fmt.Sprintf(`job=%q,mountpoint="/",fstype!="rootfs"`, storageNodeJob)
	rule := func(name, expr, forDuration, severity, summary, description string) promRule {
		return promRule{
			Alert: name, Expr: expr, For: forDuration,
			Labels:      map[string]string{"severity": severity},
			Annotations: map[string]string{"summary": summary, "description": description},
		}
	}
	group := promRuleGroup{Name: "k8s-lab-storage", Interval: "30s", Rules: []promRule{
		rule("StorageNodeDown",
			fmt.Sprintf(`(max(up{job=%q}) or vector(0)) == 0`, storageNodeJob),
			"5m", "critical", "The storage node is not reporting metrics",
			"node_exporter on the storage node has not answered a scrape for 5 minutes. Every PersistentVolume is served from this node over NFS, so this is also the blast radius of a storage outage."),
		rule("StorageNodeDiskFillingUp",
			fmt.Sprintf(`100 * node_filesystem_avail_bytes{%[1]s} / node_filesystem_size_bytes{%[1]s} < 15`, root),
			"10m", "warning", "The storage node export has less than 15% free space",
			"The filesystem backing /exports/k8s-volumes is filling up; when it is full, writes to every PVC fail. Free space or grow the disk."),
		rule("StorageNodeDiskAlmostFull",
			fmt.Sprintf(`100 * node_filesystem_avail_bytes{%[1]s} / node_filesystem_size_bytes{%[1]s} < 5`, root),
			"5m", "critical", "The storage node export has less than 5% free space",
			"The filesystem backing every PVC is nearly full; writes will start failing cluster-wide."),
		rule("PersistentVolumeFillingUp",
			`100 * (1 - kubelet_volume_stats_available_bytes / kubelet_volume_stats_capacity_bytes) > 85`,
			"10m", "warning", "A PersistentVolumeClaim is over 85% full",
			"{{ $labels.namespace }}/{{ $labels.persistentvolumeclaim }} is running low on space; its workload may fail to write."),
		rule("PersistentVolumeErrors",
			`sum by (persistentvolume) (kube_persistentvolume_status_phase{phase=~"Failed|Pending"}) > 0`,
			"5m", "warning", "A PersistentVolume is in Failed or Pending",
			"{{ $labels.persistentvolume }} is not Bound, so a claim cannot use it. Check the volume and its backing NFS export."),
	}}
	spec, err := yaml.Marshal(map[string]any{
		"apiVersion": "monitoring.coreos.com/v1",
		"kind":       "PrometheusRule",
		"metadata":   map[string]string{"name": "k8s-lab-storage", "namespace": "monitoring"},
		"spec":       map[string]any{"groups": []promRuleGroup{group}},
	})
	return string(spec), err
}
