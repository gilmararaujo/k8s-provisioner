package user

import (
	"fmt"
	"os"

	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
)

// userKubeconfig groups the inputs for writeUserKubeconfig; a struct avoids a
// run of same-typed positional string params that are easy to transpose.
type userKubeconfig struct {
	SourceKubeconfig string
	Username         string
	KeyPath          string
	CertPath         string
	OutputPath       string
}

// writeUserKubeconfig builds a standalone kubeconfig for kc.Username from the
// cluster details of kc.SourceKubeconfig plus the user's key/cert, writing it to
// kc.OutputPath. This is the only place that changes when kubeconfig layout
// changes (SRP); it has no dependency on the cluster client.
func writeUserKubeconfig(kc userKubeconfig) error {
	// Load existing kubeconfig to get cluster info
	config, err := clientcmd.LoadFromFile(kc.SourceKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	currentContext := config.CurrentContext
	if currentContext == "" {
		return fmt.Errorf("no current context in kubeconfig")
	}

	contextConfig := config.Contexts[currentContext]
	if contextConfig == nil {
		return fmt.Errorf("context not found: %s", currentContext)
	}

	clusterConfig := config.Clusters[contextConfig.Cluster]
	if clusterConfig == nil {
		return fmt.Errorf("cluster not found: %s", contextConfig.Cluster)
	}

	keyData, err := os.ReadFile(kc.KeyPath)
	if err != nil {
		return fmt.Errorf("failed to read key: %w", err)
	}

	certData, err := os.ReadFile(kc.CertPath)
	if err != nil {
		return fmt.Errorf("failed to read cert: %w", err)
	}

	clusterName := contextConfig.Cluster
	contextName := fmt.Sprintf("%s@%s", kc.Username, clusterName)

	newConfig := api.NewConfig()
	newConfig.Clusters[clusterName] = &api.Cluster{
		Server:                   clusterConfig.Server,
		CertificateAuthorityData: clusterConfig.CertificateAuthorityData,
	}
	newConfig.AuthInfos[kc.Username] = &api.AuthInfo{
		ClientCertificateData: certData,
		ClientKeyData:         keyData,
	}
	newConfig.Contexts[contextName] = &api.Context{
		Cluster:  clusterName,
		AuthInfo: kc.Username,
	}
	newConfig.CurrentContext = contextName

	if err := clientcmd.WriteToFile(*newConfig, kc.OutputPath); err != nil {
		return fmt.Errorf("failed to write kubeconfig: %w", err)
	}

	return nil
}
