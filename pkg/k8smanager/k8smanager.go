package k8smanager

import (
	"context"
	"regexp"
	"strings"
	"time"

	"k8s.io/client-go/rest"

	"github.com/fusakla/lok8/pkg/controllers"
	tsdblabels "github.com/prometheus/tsdb/labels"
	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

func New(clientset kubernetes.Interface, namespaces []string) K8sManager {
	var ctrls []controllers.PodController
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
	var pods []*v1.Pod
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
	var namespaces []string
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
	var podNames []string
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
	var containerNames []string
	pods, err := m.ListPods(labels.Everything())
	if err != nil {
		log.Error("msg", "failed to list pods", "error", err)
		return []string{}
	}
	encountered := map[string]bool{}
	for _, p := range pods {
		for _, c := range append(p.Spec.Containers, p.Spec.InitContainers...) {
			if _, ok := encountered[c.Name]; !ok {
				containerNames = append(containerNames, c.Name)
				encountered[c.Name] = true
			}
		}
	}
	return containerNames
}

func K8sLabelToPrometheusLabel(k8sLabel string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(k8sLabel, "/", "_"), "-", "_"), ".", "_")
}

func (m *K8sManager) ListPodsLabelNames() []string {
	var labelNames []string
	pods, err := m.ListPods(labels.Everything())
	if err != nil {
		log.Error("msg", "failed to list pods", "error", err)
		return []string{}
	}
	encountered := map[string]bool{}
	for _, p := range pods {
		for l := range p.Labels {
			if _, ok := encountered[l]; !ok {
				labelNames = append(labelNames, K8sLabelToPrometheusLabel(l))
				encountered[l] = true
			}
		}
	}
	return labelNames
}

func (m *K8sManager) ListPodsLabelValues(label string) []string {
	var labelValues []string
	pods, err := m.ListPods(labels.Everything())
	if err != nil {
		log.Error("msg", "failed to list pods", "error", err)
		return []string{}
	}
	encountered := map[string]bool{}
	for _, p := range pods {
		for l, v := range p.Labels {
			if K8sLabelToPrometheusLabel(l) == label {
				if _, ok := encountered[v]; !ok {
					labelValues = append(labelValues, v)
					encountered[v] = true
				}
			}
		}
	}
	return labelValues
}

type LogLabels map[string]string

type ContainerLogsOpts struct {
	LogResponseWrapper rest.ResponseWrapper
	Labels             map[string]string
}

func (m *K8sManager) NewContainerLogsOpts(ctx context.Context, namespace string, podName string, logOpts v1.PodLogOptions, labels LogLabels) ContainerLogsOpts {
	req := rest.ResponseWrapper(m.clientset.CoreV1().Pods(namespace).GetLogs(podName, &logOpts).Context(ctx))
	return ContainerLogsOpts{
		LogResponseWrapper: req,
		Labels:             labels,
	}
}

// PodLabelsToLogLabels adds pod metadata to it's k8s labels such as namespace, pod name and container name
func PodLabelsToLogLabels(pod *v1.Pod, container *v1.Container) LogLabels {
	labelsMap := LogLabels{}
	for k, v := range pod.Labels {
		labelsMap[K8sLabelToPrometheusLabel(k)] = v
	}
	labelsMap["namespace"] = pod.Namespace
	labelsMap["namespace_name"] = pod.Namespace
	labelsMap["pod"] = pod.Name
	labelsMap["pod_name"] = pod.Name
	labelsMap["container"] = container.Name
	labelsMap["container_name"] = container.Name
	return labelsMap
}

type LogQueryOpts interface {
	Selector() tsdblabels.Selector
	From() time.Time
	To() time.Time
	LinesLimit() int
	Filter() *regexp.Regexp
	TailLogs() bool
}

func (m *K8sManager) GetLogsFetchOptions(ctx context.Context, queryOpts LogQueryOpts) ([]ContainerLogsOpts, error) {
	var (
		containerLogsOptsToFetch []ContainerLogsOpts
	)
	//limitLines64 := int64(queryOpts.LinesLimit())
	sinceTime := metav1.NewTime(queryOpts.From())

	// List all pods so we can match them according to label selector.
	pods, err := m.ListPods(labels.Everything())
	if err != nil {
		log.Error("msg", "failed to list pods", "error", err)
		return containerLogsOptsToFetch, err
	}

	for _, p := range pods {
		if p.Status.Reason == "Evicted" {
			log.Debug("skipping evicted pod: ", p.Name)
			continue
		}
		// Iterate over all containers of the pod. Don't forget about init containers.
		for _, c := range append(p.Spec.Containers, p.Spec.InitContainers...) {
			// Convert pod metadata to log labels.
			logLabels := PodLabelsToLogLabels(p, &c)
			// Check if the pod matches the requested label selector.
			if !queryOpts.Selector().Matches(tsdblabels.FromMap(logLabels)) {
				continue
			}

			// Prepare log opts for the container
			logOpts := v1.PodLogOptions{
				Container:  c.Name,
				Timestamps: true,
				Follow:     queryOpts.TailLogs(),
				SinceTime:  &sinceTime,
			}

			containerLogsOptsToFetch = append(containerLogsOptsToFetch, m.NewContainerLogsOpts(ctx, p.Namespace, p.Name, logOpts, logLabels))

			// Get previous logs if the pod is younger than since time eg. lower time boundary of query.
			// We need to iterate over all container statuses to find status of actual container.
			for _, cs := range p.Status.ContainerStatuses {
				if cs.Name == c.Name {
					if cs.State.Running == nil || sinceTime.Before(&cs.State.Running.StartedAt) {
						// Add log opt to get previous container logs and label to distinguish the previous container from the actual.
						previousLogOpts := v1.PodLogOptions{
							Container:  c.Name,
							Timestamps: true,
							Follow:     queryOpts.TailLogs(),
							SinceTime:  &sinceTime,
							Previous:   true,
						}
						previousLogLabels := PodLabelsToLogLabels(p, &c)
						previousLogLabels["previous"] = "true"
						containerLogsOptsToFetch = append(containerLogsOptsToFetch, m.NewContainerLogsOpts(ctx, p.Namespace, p.Name, previousLogOpts, previousLogLabels))
					}
					break
				}
			}
		}
	}
	return containerLogsOptsToFetch, nil
}
