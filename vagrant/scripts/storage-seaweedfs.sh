#!/usr/bin/env bash
# SeaweedFS S3 on the storage node: an S3-compatible object store that lives
# OUTSIDE the cluster (like Vault, NFS and CoreDNS), so backups survive a
# `vagrant destroy` of the cluster. It is the single backup substrate for both
# the etcd snapshots (bucket "etcd-backups") and Velero (bucket "velero").
#
# Why SeaweedFS and not MinIO: MinIO stopped distributing its community binary
# at a pinnable URL (dl.min.io returns 410; only the commercial AIStor path
# serves one). SeaweedFS is a single Go binary under Apache-2.0 that speaks the
# S3 API, so it drops into the same binary+systemd pattern the other services
# on this node use.
#
# Inputs (env):
#   SEAWEEDFS_VERSION   e.g. 4.53   (GitHub release tag, read from config.yaml)
#   STORAGE_IP          the private IP the S3 endpoint binds/advertises
#   S3_PORT             S3 API port (default 8333)
#   BUCKET_ETCD         bucket for etcd snapshots (default etcd-backups)
#   BUCKET_VELERO       bucket for Velero backups (default velero)
set -euo pipefail

: "${SEAWEEDFS_VERSION:?}"
: "${STORAGE_IP:?}"
S3_PORT="${S3_PORT:-8333}"
BUCKET_ETCD="${BUCKET_ETCD:-etcd-backups}"
BUCKET_VELERO="${BUCKET_VELERO:-velero}"

ARCH=$(dpkg --print-architecture)   # arm64 | amd64 — matches SeaweedFS asset names
DATA_DIR=/var/lib/seaweedfs
CONF_DIR=/etc/seaweedfs
S3_CONFIG="$CONF_DIR/s3.json"
ADMIN_ENV="$CONF_DIR/s3-admin.env"   # root-only; the provisioner reads it to push creds to Vault

echo "=== Installing SeaweedFS ${SEAWEEDFS_VERSION} (${ARCH}) ==="
apt-get install -y curl

# Binary (the tarball ships a single `weed`), re-downloaded only when the pinned
# version changes. The per-asset .md5 is verified first: this is a third-party
# binary on the storage node.
if ! weed version 2>/dev/null | grep -q "version .*${SEAWEEDFS_VERSION} "; then
  d=$(mktemp -d); trap 'rm -rf "$d"' EXIT
  base="https://github.com/seaweedfs/seaweedfs/releases/download/${SEAWEEDFS_VERSION}"
  file="linux_${ARCH}.tar.gz"
  curl -fsSL -o "$d/$file" "$base/$file"
  curl -fsSL -o "$d/$file.md5" "$base/$file.md5"
  want=$(awk '{print $1}' "$d/$file.md5")
  got=$(md5sum "$d/$file" | awk '{print $1}')
  [ "$want" = "$got" ] || { echo "checksum mismatch: want $want got $got" >&2; exit 1; }
  tar xzf "$d/$file" -C "$d"
  install -m0755 "$d/weed" /usr/local/bin/weed
fi
weed version

useradd --system --no-create-home --shell /usr/sbin/nologin seaweedfs 2>/dev/null || true
mkdir -p "$DATA_DIR" "$CONF_DIR"
chown seaweedfs:seaweedfs "$DATA_DIR"

# S3 admin credentials: generated once and preserved across re-provisions so the
# cluster's stored endpoint keeps working. The provisioner later reads $ADMIN_ENV
# over SSH to push these into Vault (VSO for Velero, an env file for etcd-backup).
if [ ! -f "$S3_CONFIG" ]; then
  apt-get install -y openssl
  AK="seaweed-admin"
  SK=$(openssl rand -hex 24)
  cat > "$S3_CONFIG" <<EOF
{
  "identities": [
    {
      "name": "admin",
      "credentials": [{ "accessKey": "${AK}", "secretKey": "${SK}" }],
      "actions": ["Admin", "Read", "Write", "List", "Tagging"]
    }
  ]
}
EOF
  cat > "$ADMIN_ENV" <<EOF
S3_ENDPOINT=http://${STORAGE_IP}:${S3_PORT}
S3_ACCESS_KEY=${AK}
S3_SECRET_KEY=${SK}
EOF
fi
chown root:seaweedfs "$S3_CONFIG"; chmod 640 "$S3_CONFIG"
chown root:root "$ADMIN_ENV";      chmod 600 "$ADMIN_ENV"

# Systemd service: `weed server` runs master+volume+filer in one process; -s3
# exposes the S3 API on $S3_PORT. It advertises/binds the private IP so the
# cluster and the controlplane reach it.
cat > /etc/systemd/system/seaweedfs.service <<UNIT
[Unit]
Description=SeaweedFS S3 object store (lab backup substrate)
After=network-online.target
Wants=network-online.target

[Service]
User=seaweedfs
Group=seaweedfs
ExecStart=/usr/local/bin/weed server \\
  -dir=${DATA_DIR} \\
  -ip=${STORAGE_IP} \\
  -ip.bind=0.0.0.0 \\
  -filer \\
  -s3 \\
  -s3.port=${S3_PORT} \\
  -s3.config=${S3_CONFIG} \\
  -volume.max=100
Restart=on-failure
RestartSec=5
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
# weed needs a writable /tmp: glog logs there, and the master/volume/filer
# components of this single process reach each other over unix sockets in /tmp.
# PrivateTmp gives it an isolated, writable one without loosening ProtectSystem.
PrivateTmp=yes
ReadWritePaths=${DATA_DIR}
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable seaweedfs
# restart (not just start) so a re-provision actually applies unit changes.
systemctl restart seaweedfs

# Pre-create the backup buckets via the native weed shell (no extra S3 client
# needed). Wait for the S3 endpoint to answer first, so the filer is registered
# and bucket creation does not block. Idempotent: an existing bucket is a no-op.
echo "=== Waiting for the S3 endpoint, then creating buckets ==="
for _ in $(seq 1 60); do
  # Any HTTP reply (even 403 AccessDenied) means the S3/filer stack is up.
  curl -s -o /dev/null "http://${STORAGE_IP}:${S3_PORT}" && break
  sleep 2
done
for b in "$BUCKET_ETCD" "$BUCKET_VELERO"; do
  echo "s3.bucket.create -name $b" | weed shell -master="${STORAGE_IP}:9333" >/dev/null 2>&1 \
    && echo "  bucket ready: $b" || echo "  WARN: could not pre-create bucket $b (will be created on first use)"
done

echo "=== SeaweedFS S3 listening on ${STORAGE_IP}:${S3_PORT} (buckets: ${BUCKET_ETCD}, ${BUCKET_VELERO}) ==="
