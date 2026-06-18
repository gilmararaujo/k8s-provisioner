package user

import (
	"fmt"
	"os"

	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
)

// writeUserKubeconfig builds a standalone kubeconfig for username from the
// cluster details of sourceKubeconfig plus the user's key/cert, writing it to
// outputPath. This is the only place that changes when kubeconfig layout changes
// (SRP); it has no dependency on the cluster client.
func writeUserKubeconfig(sourceKubeconfig, username, keyPath, certPath, outputPath string) error {
	// Load existing kubeconfig to get cluster info
	config, err := clientcmd.LoadFromFile(sourceKubeconfig)
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

	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("failed to read key: %w", err)
	}

	certData, err := os.ReadFile(certPath)
	if err != nil {
		return fmt.Errorf("failed to read cert: %w", err)
	}

	clusterName := contextConfig.Cluster
	contextName := fmt.Sprintf("%s@%s", username, clusterName)

	newConfig := api.NewConfig()
	newConfig.Clusters[clusterName] = &api.Cluster{
		Server:                   clusterConfig.Server,
		CertificateAuthorityData: clusterConfig.CertificateAuthorityData,
	}
	newConfig.AuthInfos[username] = &api.AuthInfo{
		ClientCertificateData: certData,
		ClientKeyData:         keyData,
	}
	newConfig.Contexts[contextName] = &api.Context{
		Cluster:  clusterName,
		AuthInfo: username,
	}
	newConfig.CurrentContext = contextName

	if err := clientcmd.WriteToFile(*newConfig, outputPath); err != nil {
		return fmt.Errorf("failed to write kubeconfig: %w", err)
	}

	return nil
}
