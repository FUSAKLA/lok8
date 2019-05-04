package controllers

import (
	"time"

	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
)

type PodController struct {
	factory informers.SharedInformerFactory
	lister  corelisters.PodLister
}

func (c *PodController) Run(stopCh chan struct{}) {
	c.factory.Start(stopCh)
}

func (c *PodController) List(selector labels.Selector) ([]*v1.Pod, error) {
	return c.lister.List(selector)
}

func (c *PodController) ListPods(selector labels.Selector) ([]*v1.Pod, error) {
	return c.lister.List(selector)
}

func NewPodController(clientset kubernetes.Interface, namespace *string) PodController {
	var informerFactory informers.SharedInformerFactory
	if namespace == nil {
		log.Info("initializing global pod watcher for whole cluster")
		informerFactory = informers.NewSharedInformerFactory(clientset, time.Second*60)
	} else {
		log.Info("initializing pod watcher for namespace ", *namespace)
		informerFactory = informers.NewFilteredSharedInformerFactory(clientset, time.Second*60, *namespace, nil)
	}

	informer := informerFactory.Core().V1().Pods()
	ctrl := PodController{
		factory: informerFactory,
		lister:  informer.Lister(),
	}
	return ctrl
}
