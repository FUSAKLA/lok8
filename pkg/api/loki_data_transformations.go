package api

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func k8sMetasNamesToLabelsResponse(entities []metav1.ObjectMeta) LabelsResponse {
	labels := LabelsResponse{
		Values: []string{},
	}
	for _, e := range entities {
		labels.Values = append(labels.Values, e.Name)
	}
	return labels
}
