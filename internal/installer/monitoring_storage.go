package installer

import (
	"fmt"

	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

func (m *Monitoring) createNFSStorage() error {
	nfsServer := m.config.Storage.NFSServer
	if nfsServer == "" {
		nfsServer = "192.168.201.20" // default NFS server
	}
	nfsPath := m.config.Storage.NFSPath
	if nfsPath == "" {
		nfsPath = "/exports/k8s-volumes"
	}

	storage := fmt.Sprintf(`apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: nfs-storage
provisioner: kubernetes.io/no-provisioner
volumeBindingMode: Immediate
---
apiVersion: v1
kind: PersistentVolume
metadata:
  name: prometheus-pv
spec:
  capacity:
    storage: 10Gi
  accessModes:
    - ReadWriteOnce
  persistentVolumeReclaimPolicy: Retain
  storageClassName: nfs-storage
  nfs:
    server: %s
    path: %s/pv01
---
apiVersion: v1
kind: PersistentVolume
metadata:
  name: grafana-pv
spec:
  capacity:
    storage: 5Gi
  accessModes:
    - ReadWriteOnce
  persistentVolumeReclaimPolicy: Retain
  storageClassName: nfs-storage
  nfs:
    server: %s
    path: %s/pv02
---
apiVersion: v1
kind: PersistentVolume
metadata:
  name: loki-pv
spec:
  capacity:
    storage: 5Gi
  accessModes:
    - ReadWriteOnce
  persistentVolumeReclaimPolicy: Retain
  storageClassName: nfs-storage
  nfs:
    server: %s
    path: %s/pv03`, nfsServer, nfsPath, nfsServer, nfsPath, nfsServer, nfsPath)

	if err := executor.WriteFile("/tmp/nfs-storage.yaml", storage); err != nil {
		return err
	}

	_, err := m.exec.RunShell("kubectl apply -f /tmp/nfs-storage.yaml")
	return err
}
