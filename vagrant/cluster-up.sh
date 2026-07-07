#!/bin/sh
#
# Creates all VMs sequentially (VirtualBox requirement), then provisions
# them in parallel to reduce total cluster setup time.
#
# Usage: ./cluster-up.sh
#
# source:
# http://joemiller.me/2012/04/26/speeding-up-vagrant-with-parallel-provisioning/

MAX_PROCS=4

parallel_provision() {
    while read box; do
        echo "Provisioning '$box'. Output will be in: $box.out.txt" 1>&2
        echo $box
    done | xargs -P $MAX_PROCS -I"BOXNAME" \
        sh -c 'vagrant provision BOXNAME >BOXNAME.out.txt 2>&1 || echo "Error Occurred: BOXNAME"'
}

## -- main -- ##

# Create all VMs sequentially to avoid VirtualBox conflicts (no provisioning yet)
vagrant up --no-provision

# Storage must be provisioned first — NFS server must be up before
# Kubernetes nodes attempt to mount /mnt/nfs-storage
echo "Provisioning storage (NFS)..."
vagrant provision storage

# Provision controlplane and workers in parallel.
# Workers wait for the API server and join command internally,
# so they can start at the same time as the controlplane.
echo "Provisioning controlplane, node01, node02 in parallel..."
cat <<EOF | parallel_provision
controlplane
node01
node02
EOF

echo ""
echo "Cluster provisioning complete. Check *.out.txt for per-node logs."
