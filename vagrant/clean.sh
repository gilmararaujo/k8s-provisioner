#!/bin/bash
#
# Cleans up the Vagrant and VirtualBox environment
# Ensures a clean installation of the Kubernetes cluster
#

set -e

cd "$(dirname "$0")"

echo "==========================================="
echo "  Vagrant/VirtualBox Environment Cleanup"
echo "==========================================="
echo ""

# 1. Destroy Vagrant VMs
echo "[1/5] Destroying Vagrant VMs..."
vagrant destroy -f 2>/dev/null || true

# 2. Remove the local .vagrant directory
echo "[2/5] Removing local metadata (.vagrant)..."
rm -rf .vagrant

# 3. Clear the Vagrant temporary cache
echo "[3/5] Clearing Vagrant temporary cache..."
rm -rf ~/.vagrant.d/tmp/* 2>/dev/null || true

# 4. Remove orphaned VirtualBox VMs
echo "[4/5] Removing VirtualBox VMs..."
for vm in "Storage" "Master" "Node01" "Node02"; do
  if VBoxManage showvminfo "$vm" &>/dev/null; then
    echo "  - Removing $vm..."
    VBoxManage controlvm "$vm" poweroff 2>/dev/null || true
    sleep 1
    VBoxManage unregistervm "$vm" --delete 2>/dev/null || true
  fi
done

# 5. Remove the old box (optional)
echo "[5/5] Checking box bento/debian-13..."
if vagrant box list | grep -q "bento/debian-13"; then
  read -p "  Remove the bento/debian-13 box to download it again? (y/N): " choice
  if [[ "$choice" =~ ^[Yy]$ ]]; then
    vagrant box remove bento/debian-13 --all --force 2>/dev/null || true
    echo "  - Box removed!"
  else
    echo "  - Box kept."
  fi
fi

echo ""
echo "==========================================="
echo "  Cleanup complete!"
echo "==========================================="
echo ""
echo "To create the cluster again, run:"
echo ""
echo "  vagrant up"
echo ""
