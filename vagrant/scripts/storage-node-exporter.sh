#!/usr/bin/env bash
# node_exporter on the storage node: host and filesystem metrics for the disk
# that backs every PersistentVolume over NFS (/exports/k8s-volumes). The storage
# node runs outside the cluster (like Vault and the lab DNS), so Prometheus
# scrapes it as a static target — see internal/installer/monitoring_storage_node.go
# (job "storage-node"), which also carries the Storage dashboard and alerts.
#
# Inputs (env): NODE_EXPORTER_VERSION  (e.g. v1.12.1)
set -euo pipefail

: "${NODE_EXPORTER_VERSION:?}"
ARCH=$(dpkg --print-architecture)
V=${NODE_EXPORTER_VERSION#v}

echo "=== Installing node_exporter ${NODE_EXPORTER_VERSION} (${ARCH}) ==="
apt-get install -y curl

# Binary, re-downloaded only when the pinned version changes. The checksum is
# verified before install: this is a third-party binary on the storage node.
if ! node_exporter --version 2>/dev/null | grep -q "version ${V}"; then
  d=$(mktemp -d); trap 'rm -rf "$d"' EXIT
  base="https://github.com/prometheus/node_exporter/releases/download/v${V}"
  file="node_exporter-${V}.linux-${ARCH}.tar.gz"
  curl -fsSL -o "$d/$file" "$base/$file"
  curl -fsSL -o "$d/sha256sums.txt" "$base/sha256sums.txt"
  ( cd "$d" && grep " $file\$" sha256sums.txt | sha256sum -c - )
  tar xzf "$d/$file" -C "$d"
  install -m0755 "$d/node_exporter-${V}.linux-${ARCH}/node_exporter" /usr/local/bin/node_exporter
fi

useradd --system --no-create-home --shell /usr/sbin/nologin node_exporter 2>/dev/null || true

cat > /etc/systemd/system/node_exporter.service <<'UNIT'
[Unit]
Description=Prometheus node_exporter (storage node metrics)
After=network-online.target
Wants=network-online.target

[Service]
User=node_exporter
Group=node_exporter
ExecStart=/usr/local/bin/node_exporter --web.listen-address=:9100
Restart=on-failure
RestartSec=5
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable --now node_exporter

echo "=== node_exporter listening on :9100 ==="
