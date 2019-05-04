// json.go
package main

import (
	"net/http"
	"os"

	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"
	kingpin "gopkg.in/alecthomas/kingpin.v2"

	"github.com/fusakla/lok8/pkg/api"
	"github.com/fusakla/lok8/pkg/k8smanager"
	"github.com/fusakla/lok8/pkg/kubecfg"
)

func setupLogging(debug bool) {
	log.SetOutput(os.Stdout)
	log.SetFormatter(&log.TextFormatter{
		DisableColors: true,
		FullTimestamp: true,
	})
	if debug {
		log.SetLevel(log.DebugLevel)
	}
}

func main() {

	var (
		config_file = kingpin.Flag("k8s-config", "Kubernetes client configuration file.").Short('c').String()
		debug       = kingpin.Flag("debug", "Enable debug logging.").Short('d').Bool()
		namespaces  = kingpin.Flag("namespace", "Restrict the lok8 to specified namespace. Can be repeated.").Short('n').Strings()
	)
	kingpin.Parse()
	setupLogging(*debug)

	log.Info("Lok8 server is starting!")

	apiClientset, err := kubecfg.GetK8sClient(config_file)
	if err != nil {
		log.Fatal("Failed to load k8s config error: ", err)
	}

	stopChannel := make(chan struct{})

	k8sMgr := k8smanager.New(apiClientset, *namespaces)
	k8sMgr.Start(stopChannel)

	baseRouter := mux.NewRouter()
	apiRouter := baseRouter.PathPrefix("/api").Subrouter()
	api.NewLokiApiInRouter(apiRouter, k8sMgr)

	log.Info("Listening on http://0.0.0.0:3001")
	http.ListenAndServe(":3001", handlers.LoggingHandler(os.Stdout, baseRouter))
}
