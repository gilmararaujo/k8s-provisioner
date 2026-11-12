#!/usr/bin/env bash
# etcd snapshot backup, run by the etcd-backup.timer on the control plane.
# Takes a snapshot with the etcd pod's OWN etcdctl (the control-plane etcd image
# is distroless, so we exec etcdctl directly — no shell in the container) writing
# into /var/lib/etcd, which is a hostPath, so the file lands on this host. The
# snapshot is then uploaded to the SeaweedFS S3 bucket on the storage node and
# old snapshots are pruned locally and in S3.
#
# Credentials and settings come from /etc/etcd-backup/s3.env (written by the
# EtcdBackup installer from the S3 creds it reads out of Vault):
#   S3_ENDPOINT S3_ACCESS_KEY S3_SECRET_KEY S3_BUCKET RETENTION_DAYS
set -euo pipefail

# shellcheck source=/dev/null
. /etc/etcd-backup/s3.env
export KUBECONFIG=/etc/kubernetes/admin.conf
export AWS_ACCESS_KEY_ID="$S3_ACCESS_KEY"
export AWS_SECRET_ACCESS_KEY="$S3_SECRET_KEY"
export AWS_DEFAULT_REGION=us-east-1

LOCAL_DIR=/var/backups/etcd
TS=$(date +%Y%m%d-%H%M%S)
SNAP="etcd-${TS}.db"
mkdir -p "$LOCAL_DIR"

POD=$(kubectl -n kube-system get pod -l component=etcd -o jsonpath='{.items[0].metadata.name}')
[ -n "$POD" ] || { echo "no etcd pod found" >&2; exit 1; }

echo "=== Snapshot $SNAP via $POD ==="
kubectl -n kube-system exec "$POD" -- etcdctl \
  --endpoints=https://127.0.0.1:2379 \
  --cacert=/etc/kubernetes/pki/etcd/ca.crt \
  --cert=/etc/kubernetes/pki/etcd/server.crt \
  --key=/etc/kubernetes/pki/etcd/server.key \
  snapshot save "/var/lib/etcd/${SNAP}"

# /var/lib/etcd is a hostPath mount, so the snapshot is now on this host.
mv "/var/lib/etcd/${SNAP}" "${LOCAL_DIR}/${SNAP}"
[ -s "${LOCAL_DIR}/${SNAP}" ] || { echo "snapshot is empty" >&2; exit 1; }
echo "  local: ${LOCAL_DIR}/${SNAP} ($(du -h "${LOCAL_DIR}/${SNAP}" | cut -f1))"

echo "=== Uploading to s3://${S3_BUCKET}/${SNAP} ==="
aws --endpoint-url "$S3_ENDPOINT" s3 cp "${LOCAL_DIR}/${SNAP}" "s3://${S3_BUCKET}/${SNAP}"

# Retention is best-effort: the snapshot above already succeeded, so a prune
# hiccup must not fail the unit. Filenames carry a YYYYMMDD prefix, so the cutoff
# is a plain integer compare — no parsing of `ls` dates.
prune() {
  local cutoff key d
  cutoff=$(date -d "-${RETENTION_DAYS} days" +%Y%m%d)
  find "$LOCAL_DIR" -name 'etcd-*.db' -type f | while read -r f; do
    d=$(basename "$f" | sed -n 's/^etcd-\([0-9]\{8\}\)-.*/\1/p')
    [ -n "$d" ] && [ "$d" -lt "$cutoff" ] && rm -f "$f" && echo "  pruned local $f"
  done
  aws --endpoint-url "$S3_ENDPOINT" s3 ls "s3://${S3_BUCKET}/" | awk '{print $4}' | while read -r key; do
    [ -z "$key" ] && continue
    d=$(echo "$key" | sed -n 's/^etcd-\([0-9]\{8\}\)-.*/\1/p')
    [ -n "$d" ] && [ "$d" -lt "$cutoff" ] && aws --endpoint-url "$S3_ENDPOINT" s3 rm "s3://${S3_BUCKET}/${key}" && echo "  pruned s3 $key"
  done
}
prune || echo "WARN: retention pass had issues (snapshot + upload still succeeded)"

echo "=== etcd backup done: ${SNAP} ==="
