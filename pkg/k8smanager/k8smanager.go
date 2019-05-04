package k8smanager

import (
	"bytes"
	"io"
	"strings"
	"time"

	"github.com/fusakla/lok8/pkg/controllers"
	tsdb_labels "github.com/prometheus/tsdb/labels"
	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

func New(clientset kubernetes.Interface, namespaces []string) K8sManager {
	ctrls := []controllers.PodController{}
	if len(namespaces) == 0 {
		ctrls = append(
			ctrls,
			controllers.NewPodController(clientset, nil),
		)
	} else {
		for _, n := range namespaces {
			ctrls = append(
				ctrls,
				controllers.NewPodController(clientset, &n),
			)
		}
	}

	return K8sManager{
		clientset:      clientset,
		podControllers: ctrls,
	}
}

type K8sManager struct {
	clientset      kubernetes.Interface
	podControllers []controllers.PodController
}

func (m *K8sManager) Start(stopCh chan struct{}) {
	log.Info("Starting k8s watchers")
	for _, c := range m.podControllers {
		c.Run(stopCh)
	}
}

func (m *K8sManager) ListPods(selector labels.Selector) ([]*v1.Pod, error) {
	pods := []*v1.Pod{}
	for _, c := range m.podControllers {
		listedPods, err := c.List(selector)
		if err != nil {
			log.Error("failed to list pods")
			return []*v1.Pod{}, nil
		}
		pods = append(pods, listedPods...)
	}
	return pods, nil
}

func (m *K8sManager) ListNamespaces() []string {
	namespaces := []string{}
	pods, err := m.ListPods(labels.Everything())
	if err != nil {
		log.Error("msg", "failed to list pods", "error", err)
		return []string{}
	}
	encountered := map[string]bool{}
	for _, p := range pods {
		if _, ok := encountered[p.Namespace]; !ok {
			namespaces = append(namespaces, p.Namespace)
			encountered[p.Namespace] = true
		}
	}
	return namespaces
}

func (m *K8sManager) ListPodNames() []string {
	podNames := []string{}
	pods, err := m.ListPods(labels.Everything())
	if err != nil {
		log.Error("msg", "failed to list pods", "error", err)
		return []string{}
	}
	for _, p := range pods {
		podNames = append(podNames, p.Name)
	}
	return podNames
}

func (m *K8sManager) ListContainerNames() []string {
	containerNames := []string{}
	pods, err := m.ListPods(labels.Everything())
	if err != nil {
		log.Error("msg", "failed to list pods", "error", err)
		return []string{}
	}
	encountered := map[string]bool{}
	for _, p := range pods {
		for _, c := range p.Spec.Containers {
			if _, ok := encountered[c.Name]; !ok {
				containerNames = append(containerNames, c.Name)
				encountered[c.Name] = true
			}
		}
	}
	return containerNames
}

func (m *K8sManager) ListPodsLabelNames() []string {
	labelNames := []string{}
	pods, err := m.ListPods(labels.Everything())
	if err != nil {
		log.Error("msg", "failed to list pods", "error", err)
		return []string{}
	}
	encountered := map[string]bool{}
	for _, p := range pods {
		for l, _ := range p.Labels {
			if _, ok := encountered[l]; !ok {
				labelNames = append(labelNames, l)
				encountered[l] = true
			}
		}
	}
	return labelNames
}

func (m *K8sManager) ListPodsLabelValues(lable string) []string {
	labelValues := []string{}
	pods, err := m.ListPods(labels.Everything())
	if err != nil {
		log.Error("msg", "failed to list pods", "error", err)
		return []string{}
	}
	encountered := map[string]bool{}
	for _, p := range pods {
		for l, v := range p.Labels {
			if l == lable {
				if _, ok := encountered[v]; !ok {
					labelValues = append(labelValues, v)
					encountered[v] = true
				}
			}
		}
	}
	return labelValues
}

type K8sLogs struct {
	Labels map[string]string
	Logs   []string
}

func (m *K8sManager) getK8sLogs(namespace string, pod string, opts v1.PodLogOptions) []string {
	req := m.clientset.CoreV1().Pods(namespace).GetLogs(pod, &opts)
	podLogs, err := req.Stream()
	if err != nil {
		log.Error("failed to read k8s logs for pod ", pod)
		return []string{}
	}
	defer podLogs.Close()
	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, podLogs)
	if err != nil {
		log.Error("failed to read k8s logs stream for pod ", pod)
		return []string{}
	}
	return strings.Split(buf.String(), "\n")
}

func (m *K8sManager) GetLogs(labelSelector tsdb_labels.Selector, since time.Time, linesLimit int64) []K8sLogs {
	logStreams := []K8sLogs{}
	pods, err := m.ListPods(labels.Everything())
	if err != nil {
		log.Error("msg", "failed to list pods", "error", err)
		return []K8sLogs{}
	}
	for _, p := range pods {
		for _, c := range p.Spec.Containers {
			// Check the pod matches selector
			labelsMap := map[string]string{}
			for k, v := range p.Labels {
				labelsMap[k] = v
			}
			labelsMap["namespace"] = p.Namespace
			labelsMap["pod"] = p.Name
			labelsMap["container"] = c.Name
			if !labelSelector.Matches(tsdb_labels.FromMap(labelsMap)) {
				continue
			}

			sinceTime := metav1.NewTime(since)
			logOpts := v1.PodLogOptions{
				Container:  c.Name,
				Timestamps: true,
				SinceTime:  &sinceTime,
				TailLines:  &linesLimit,
			}
			// Get current logs
			stream := m.getK8sLogs(p.Namespace, p.Name, logOpts)

			// Get previous logs if pod is younger than
			for _, cs := range p.Status.ContainerStatuses {
				if cs.Name == c.Name {
					if cs.State.Running.StartedAt.Before(&sinceTime) {
						logOpts.Previous = true
						previousLogs := m.getK8sLogs(p.Namespace, p.Name, logOpts)
						stream = append(stream, previousLogs...)
						break
					}
				}
			}

			logStreams = append(
				logStreams,
				K8sLogs{
					Labels: labelsMap,
					Logs:   stream,
				},
			)

		}
	}
	return logStreams
}
