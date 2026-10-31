#!/usr/bin/env bash
# Lab DNS on the storage node: etcd (record store) + CoreDNS (resolver).
#
# ExternalDNS (in the cluster) writes records for every Istio Gateway host into
# etcd under /skydns; CoreDNS serves them for $DOMAIN, plus static records for
# the lab nodes, and forwards everything else upstream. Runs outside the
# cluster, like Vault, so DNS survives cluster rebuilds.
#
# Inputs (env): DOMAIN, STORAGE_IP, ETCD_VERSION, COREDNS_VERSION, LAB_HOSTS
#   LAB_HOSTS: newline-separated "ip name" pairs for the static node records.
set -euo pipefail

: "${DOMAIN:?}" "${STORAGE_IP:?}" "${ETCD_VERSION:?}" "${COREDNS_VERSION:?}" "${LAB_HOSTS:?}"
ARCH=$(dpkg --print-architecture)
TLS_DIR=/etc/etcd/tls

echo "=== Installing lab DNS (etcd ${ETCD_VERSION} + CoreDNS ${COREDNS_VERSION}) for ${DOMAIN} ==="
apt-get install -y curl openssl dnsutils

# --- Binaries (re-downloaded only when the pinned version changes) ---
if ! etcd --version 2>/dev/null | grep -q "etcd Version: ${ETCD_VERSION#v}"; then
  curl -fsSL "https://github.com/etcd-io/etcd/releases/download/${ETCD_VERSION}/etcd-${ETCD_VERSION}-linux-${ARCH}.tar.gz" \
    | tar xz -C /usr/local/bin --strip-components=1 "etcd-${ETCD_VERSION}-linux-${ARCH}/etcd" "etcd-${ETCD_VERSION}-linux-${ARCH}/etcdctl"
fi
if ! coredns -version 2>/dev/null | grep -q "CoreDNS-${COREDNS_VERSION}"; then
  curl -fsSL "https://github.com/coredns/coredns/releases/download/v${COREDNS_VERSION}/coredns_${COREDNS_VERSION}_linux_${ARCH}.tgz" \
    | tar xz -C /usr/local/bin coredns
fi

useradd --system --home /var/lib/etcd --shell /bin/false etcd 2>/dev/null || true
useradd --system --no-create-home --shell /bin/false coredns 2>/dev/null || true
mkdir -p /var/lib/etcd /etc/coredns $TLS_DIR
chown etcd:etcd /var/lib/etcd && chmod 700 /var/lib/etcd

# --- mTLS for etcd (generated once; ExternalDNS gets its client cert over SSH) ---
issue_cert() { # name cn ext
  openssl req -quiet -newkey rsa:2048 -sha256 -nodes -keyout "$TLS_DIR/$1.key" -out "/tmp/$1.csr" -subj "/CN=$2"
  printf '%s\n' "$3" > "/tmp/$1.ext"
  openssl x509 -req -in "/tmp/$1.csr" -sha256 -days 825 \
    -CA $TLS_DIR/ca.crt -CAkey $TLS_DIR/ca.key -CAcreateserial \
    -out "$TLS_DIR/$1.crt" -extfile "/tmp/$1.ext"
  rm -f "/tmp/$1.csr" "/tmp/$1.ext"
}
if [ ! -f $TLS_DIR/ca.crt ]; then
  openssl req -quiet -x509 -newkey rsa:4096 -sha256 -days 3650 -nodes \
    -keyout $TLS_DIR/ca.key -out $TLS_DIR/ca.crt -subj "/CN=k8s-lab etcd CA" \
    -addext "basicConstraints=critical,CA:TRUE" -addext "keyUsage=critical,keyCertSign,cRLSign"
  issue_cert server storage "subjectAltName=IP:${STORAGE_IP},IP:127.0.0.1,DNS:storage,DNS:localhost
extendedKeyUsage=serverAuth
keyUsage=critical,digitalSignature,keyEncipherment"
  for client in coredns external-dns; do
    issue_cert "$client" "$client" "extendedKeyUsage=clientAuth
keyUsage=critical,digitalSignature,keyEncipherment"
  done
fi
chmod 600 $TLS_DIR/*.key && chmod 644 $TLS_DIR/*.crt
chown etcd:etcd $TLS_DIR/server.key $TLS_DIR/server.crt
chown coredns:coredns $TLS_DIR/coredns.key $TLS_DIR/coredns.crt

# --- etcd: single member; peers stay on loopback, clients need a cert from the CA ---
cat > /etc/systemd/system/etcd.service <<EOF
[Unit]
Description=etcd (lab DNS record store)
After=network-online.target
Wants=network-online.target

[Service]
User=etcd
Group=etcd
ExecStart=/usr/local/bin/etcd \\
  --name storage \\
  --data-dir /var/lib/etcd \\
  --listen-peer-urls http://127.0.0.1:2380 \\
  --initial-advertise-peer-urls http://127.0.0.1:2380 \\
  --initial-cluster storage=http://127.0.0.1:2380 \\
  --listen-client-urls https://127.0.0.1:2379,https://${STORAGE_IP}:2379 \\
  --advertise-client-urls https://${STORAGE_IP}:2379 \\
  --cert-file $TLS_DIR/server.crt --key-file $TLS_DIR/server.key \\
  --client-cert-auth --trusted-ca-file $TLS_DIR/ca.crt
Restart=on-failure
RestartSec=5
LimitNOFILE=65536
NoNewPrivileges=yes

[Install]
WantedBy=multi-user.target
EOF

# --- CoreDNS: static node records, then ExternalDNS records from etcd ---
# Metrics on :9153 (both server blocks share the listener) are scraped by the
# cluster Prometheus for the lab dashboard.
printf '%s\n' "$LAB_HOSTS" | awk -v d="$DOMAIN" 'NF==2 {print $1, $2"."d}' > /etc/coredns/lab.hosts
echo "${STORAGE_IP} vault.${DOMAIN}" >> /etc/coredns/lab.hosts

cat > /etc/coredns/Corefile <<EOF
${DOMAIN}:53 {
    bind ${STORAGE_IP} 127.0.0.1
    hosts /etc/coredns/lab.hosts {
        fallthrough
    }
    etcd {
        path /skydns
        endpoint https://127.0.0.1:2379
        tls $TLS_DIR/coredns.crt $TLS_DIR/coredns.key $TLS_DIR/ca.crt
    }
    cache 30
    errors
    log
    prometheus ${STORAGE_IP}:9153
}
.:53 {
    bind ${STORAGE_IP} 127.0.0.1
    forward . 8.8.8.8 1.1.1.1
    cache 30
    errors
    prometheus ${STORAGE_IP}:9153
}
EOF

cat > /etc/systemd/system/coredns.service <<'EOF'
[Unit]
Description=CoreDNS (lab DNS)
After=network-online.target etcd.service
Wants=network-online.target etcd.service

[Service]
User=coredns
Group=coredns
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
ExecStart=/usr/local/bin/coredns -conf /etc/coredns/Corefile
ExecReload=/bin/kill -SIGUSR1 $MAINPID
Restart=on-failure
RestartSec=5
NoNewPrivileges=yes

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable etcd coredns
systemctl restart etcd coredns

for _ in $(seq 1 15); do
  if dig +short @"${STORAGE_IP}" "storage.${DOMAIN}" 2>/dev/null | grep -q "${STORAGE_IP}"; then
    echo "=== Lab DNS ready: ${DOMAIN} served at ${STORAGE_IP}:53 ==="
    exit 0
  fi
  sleep 2
done
echo "Lab DNS did not answer for storage.${DOMAIN}" >&2
systemctl --no-pager status etcd coredns || true
exit 1
