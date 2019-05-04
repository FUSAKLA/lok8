package kubecfg

import (
	"os"

	log "github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func clientConfigFromPath(path string) (*rest.Config, error) {
	config, err := clientcmd.BuildConfigFromFlags("", path)
	if err != nil {
		return nil, err
	}
	return config, nil
}

func defaultConfig() (*rest.Config, error) {
	var config *rest.Config
	configPath := os.Getenv("KUBECONFIG")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		config, err = clientConfigFromPath(configPath)
		if err != nil {
			return nil, err
		}
	} else {
		config, err = rest.InClusterConfig()
		if err != nil {
			return nil, err
		}
	}
	return config, nil
}

func GetK8sClient(path *string) (*kubernetes.Clientset, error) {
	var (
		restConfig *rest.Config
		err        error
	)
	log.Info("Loading k8s configuration")
	if path != nil {
		restConfig, err = clientConfigFromPath(*path)
	} else {
		restConfig, err = defaultConfig()
	}
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		log.Fatal(err)
	}
	return clientset, nil
}
