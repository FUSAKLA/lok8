package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fusakla/lok8/pkg/k8smanager"
	"github.com/gorilla/mux"
	"github.com/prometheus/prometheus/pkg/labels"
	tsdb_labels "github.com/prometheus/tsdb/labels"
	log "github.com/sirupsen/logrus"

	"github.com/grafana/loki/pkg/parser"
)

type LabelsResponse struct {
	Values []string `json:"values"`
}

type QueryResponseEntry struct {
	Timestamp string `json:"ts"`
	Line      string `json:"line"`
}

type QueryResponseStream struct {
	Labels  string               `json:"labels"`
	Entries []QueryResponseEntry `json:"entries"`
}

type QueryResponse struct {
	Streams []QueryResponseStream `json:"streams"`
}

func NewLokiApiInRouter(r *mux.Router, k8sMgr k8smanager.K8sManager) *mux.Router {
	lokiApi := NewLokiApi(k8sMgr)
	r.HandleFunc("/prom/label", lokiApi.getLabels)
	r.HandleFunc("/prom/label/{label}/values", lokiApi.getLabelValues)
	r.HandleFunc("/prom/query", lokiApi.query)
	return r
}

func NewLokiApi(k8sMgr k8smanager.K8sManager) lokiApi {
	return lokiApi{
		k8sManager: k8sMgr,
	}
}

func writeJsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

type lokiApi struct {
	k8sManager k8smanager.K8sManager
}

func (a *lokiApi) getLabels(w http.ResponseWriter, r *http.Request) {
	resp := LabelsResponse{
		Values: []string{
			"namespace",
			"instance",
			"pod",
			"pod_name",
			"container",
		},
	}
	additionalPodLbels := a.k8sManager.ListPodsLabelNames()
	resp.Values = append(resp.Values, additionalPodLbels...)
	writeJsonResponse(w, resp)
}

func (a *lokiApi) getLabelValues(w http.ResponseWriter, r *http.Request) {
	var resp LabelsResponse
	vars := mux.Vars(r)
	if vars["label"] == "namespace" {
		resp = LabelsResponse{
			Values: a.k8sManager.ListNamespaces(),
		}
	} else if vars["label"] == "instance" || vars["label"] == "pod" || vars["label"] == "pod_name" {
		resp = LabelsResponse{
			Values: a.k8sManager.ListPodNames(),
		}
	} else if vars["label"] == "container" {
		resp = LabelsResponse{
			Values: a.k8sManager.ListContainerNames(),
		}
	} else {
		resp = LabelsResponse{
			Values: a.k8sManager.ListPodsLabelValues(vars["label"]),
		}
	}
	writeJsonResponse(w, resp)
}

func convertMatcher(m *labels.Matcher) tsdb_labels.Matcher {
	switch m.Type {
	case labels.MatchEqual:
		return tsdb_labels.NewEqualMatcher(m.Name, m.Value)

	case labels.MatchNotEqual:
		return tsdb_labels.Not(tsdb_labels.NewEqualMatcher(m.Name, m.Value))

	case labels.MatchRegexp:
		res, err := tsdb_labels.NewRegexpMatcher(m.Name, "^(?:"+m.Value+")$")
		if err != nil {
			panic(err)
		}
		return res

	case labels.MatchNotRegexp:
		res, err := tsdb_labels.NewRegexpMatcher(m.Name, "^(?:"+m.Value+")$")
		if err != nil {
			panic(err)
		}
		return tsdb_labels.Not(res)
	}
	panic("storage.convertMatcher: invalid matcher type")
}

func (a *lokiApi) query(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()["query"]
	startTs := r.URL.Query()["start"]
	endTs := r.URL.Query()["end"]
	limit := r.URL.Query()["limit"]
	regexpParams := r.URL.Query()["regexp"]

	var (
		reg *regexp.Regexp = nil
		err error
	)
	if len(regexpParams) > 0 {
		reg, err = regexp.Compile(regexpParams[0])
		if err != nil {
			http.Error(w, "Invalid regex param", http.StatusBadRequest)
			return
		}
	}

	if len(query) < 1 || len(startTs) < 1 || len(endTs) < 1 || len(limit) < 1 {
		http.Error(w, "Missing query parameter", http.StatusBadRequest)
		return
	}

	matchers, err := parser.Matchers(query[0])
	if err != nil {
		log.Error("failed to parse label matchers: ", query[0])
		http.Error(w, "Malformed query parameter", http.StatusBadRequest)
		return
	}

	sinceNs, err := strconv.ParseInt(startTs[0], 10, 64)
	if err != nil {
		log.Error("failed to parse start timestamp: ", startTs[0])
		http.Error(w, "Malformed start parameter", http.StatusBadRequest)
		return
	}
	since := time.Unix(0, sinceNs)

	toNs, err := strconv.ParseInt(endTs[0], 10, 64)
	if err != nil {
		log.Error("failed to parse to timestamp: ", startTs[0])
		http.Error(w, "Malformed to parameter", http.StatusBadRequest)
		return
	}
	to := time.Unix(0, toNs)

	lineLimit, err := strconv.ParseInt(limit[0], 10, 64)
	if err != nil {
		log.Error("failed to parse lines limit: ", limit[0])
		http.Error(w, "Malformed limit parameter", http.StatusBadRequest)
		return
	}

	selector := tsdb_labels.Selector{}
	for _, m := range matchers {
		selector = append(selector, convertMatcher(m))
	}
	logStreams := a.k8sManager.GetLogs(selector, since, lineLimit)
	response := QueryResponse{
		Streams: []QueryResponseStream{},
	}

	for _, l := range logStreams {
		stream := QueryResponseStream{
			Labels:  labels.FromMap(l.Labels).String(),
			Entries: []QueryResponseEntry{},
		}
		for _, line := range l.Logs {
			data := strings.SplitN(line, " ", 2)
			if len(data) < 2 {
				continue
			}
			t, err := time.Parse(time.RFC3339Nano, data[0])
			if err != nil {
				continue
			}
			if !t.Before(to) {
				continue
			}
			if reg != nil && !reg.MatchString(data[1]) {
				continue
			}
			stream.Entries = append(stream.Entries, QueryResponseEntry{
				Timestamp: data[0],
				Line:      data[1],
			})
		}
		response.Streams = append(
			response.Streams,
			stream,
		)
	}
	writeJsonResponse(w, response)
}
