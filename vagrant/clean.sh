#!/bin/bash
#
# Script para limpar ambiente Vagrant e VirtualBox
# Garante uma instalação limpa do cluster Kubernetes
#

set -e

cd "$(dirname "$0")"

echo "==========================================="
echo "  Limpeza do Ambiente Vagrant/VirtualBox"
echo "==========================================="
echo ""

# 1. Destruir VMs do Vagrant
echo "[1/5] Destruindo VMs do Vagrant..."
vagrant destroy -f 2>/dev/null || true

# 2. Remover diretório .vagrant local
echo "[2/5] Removendo metadados locais (.vagrant)..."
rm -rf .vagrant

# 3. Limpar cache temporário do Vagrant
echo "[3/5] Limpando cache temporário do Vagrant..."
rm -rf ~/.vagrant.d/tmp/* 2>/dev/null || true

# 4. Remover VMs órfãs do VirtualBox
echo "[4/5] Removendo VMs do VirtualBox..."
for vm in "Storage" "Master" "Node01" "Node02"; do
  if VBoxManage showvminfo "$vm" &>/dev/null; then
    echo "  - Removendo $vm..."
    VBoxManage controlvm "$vm" poweroff 2>/dev/null || true
    sleep 1
    VBoxManage unregistervm "$vm" --delete 2>/dev/null || true
  fi
done

# 5. Remover box antiga (opcional)
echo "[5/5] Verificando box bento/debian-12..."
if vagrant box list | grep -q "bento/debian-12"; then
  read -p "  Deseja remover a box bento/debian-12 para baixar novamente? (s/N): " choice
  if [[ "$choice" =~ ^[Ss]$ ]]; then
    vagrant box remove bento/debian-12 --all --force 2>/dev/null || true
    echo "  - Box removida!"
  else
    echo "  - Box mantida."
  fi
fi

echo ""
echo "==========================================="
echo "  Limpeza concluída!"
echo "==========================================="
echo ""
echo "Para criar o cluster novamente, execute:"
echo ""
echo "  vagrant up"
echo ""