package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/weaveworks/common/httpgrpc/server"

	"github.com/fusakla/lok8/pkg/logpipeline"

	"github.com/fusakla/lok8/pkg/k8smanager"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/prometheus/prometheus/pkg/labels"
	tsdblabels "github.com/prometheus/tsdb/labels"
	log "github.com/sirupsen/logrus"

	"github.com/grafana/loki/pkg/parser"
)

type LabelsResponse struct {
	Values []string `json:"values"`
}

type QueryResponseLogTimestamp struct {
	time.Time
}

func (t QueryResponseLogTimestamp) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.Format(time.RFC3339Nano))
}

type QueryResponseLog struct {
	Timestamp QueryResponseLogTimestamp `json:"ts"`
	Log       string                    `json:"line"`
}

type QueryResponseStream struct {
	Labels  string             `json:"labels"`
	Entries []QueryResponseLog `json:"entries"`
}

func (s *QueryResponseStream) AddEntry(logLine *logpipeline.LogLine) {
	s.Entries = append(s.Entries, QueryResponseLog{
		Timestamp: QueryResponseLogTimestamp(logLine.Timestamp),
		Log:       logLine.Log,
	})
}

type QueryResponse struct {
	Streams []QueryResponseStream `json:"streams"`
}

type LogQueryOpts struct {
	selector          tsdblabels.Selector
	from              time.Time
	to                time.Time
	linesLimit        int
	filter            *regexp.Regexp
	tailLogs          bool
	tailFlushInterval time.Duration
}

func (o *LogQueryOpts) Selector() tsdblabels.Selector {
	return o.selector
}

func (o *LogQueryOpts) From() time.Time {
	return o.from
}

func (o *LogQueryOpts) To() time.Time {
	return o.to
}

func (o *LogQueryOpts) LinesLimit() int {
	return o.linesLimit
}

func (o *LogQueryOpts) Filter() *regexp.Regexp {
	return o.filter
}

func (o *LogQueryOpts) TailLogs() bool {
	return o.tailLogs
}

func (o *LogQueryOpts) TailFlushInterval() time.Duration {
	return o.tailFlushInterval
}

func NewLogQueryOptsFromUrl(url *url.URL) (*LogQueryOpts, error) {
	query := url.Query()["query"]
	startTs := url.Query()["start"]
	endTs := url.Query()["end"]
	limit := url.Query()["limit"]
	regexpParams := url.Query()["regexp"]

	var (
		reg *regexp.Regexp = nil
		err error
	)
	if len(regexpParams) > 0 {
		reg, err = regexp.Compile(regexpParams[0])
		if err != nil {
			return nil, errors.New("invalid regex param")
		}
	}

	if len(query) < 1 {
		return nil, errors.New("missing query parameter")
	}

	matchers, err := parser.Matchers(query[0])
	if err != nil {
		log.Error("failed to parse label matchers: ", query[0])
		return nil, errors.New("malformed query parameter")
	}

	var since time.Time
	if len(startTs) < 1 {
		since = time.Now().Add(-time.Hour)
	} else {
		sinceNs, err := strconv.ParseInt(startTs[0], 10, 64)
		if err != nil {
			log.Error("failed to parse start timestamp: ", startTs[0])
			return nil, errors.New("malformed start parameter")
		}
		since = time.Unix(0, sinceNs)
	}

	var to time.Time
	if len(endTs) < 1 {
		to = time.Now()
	} else {
		toNs, err := strconv.ParseInt(endTs[0], 10, 64)
		to = time.Unix(0, toNs)
		if err != nil {
			log.Error("failed to parse to timestamp: ", startTs[0])
			return nil, errors.New("malformed to parameter")
		}
	}

	var lineLimit int64
	if len(limit) < 1 {
		lineLimit = 1000
	} else {
		lineLimit, err = strconv.ParseInt(limit[0], 10, 64)
		if err != nil {
			log.Error("failed to parse lines limit: ", limit[0])
			return nil, errors.New("malformed limit parameter")
		}
	}

	flushInterval, _ := time.ParseDuration("1s")

	selector := tsdblabels.Selector{}
	for _, m := range matchers {
		selector = append(selector, convertMatcher(m))
	}
	return &LogQueryOpts{
		selector:          selector,
		from:              since,
		to:                to,
		linesLimit:        int(lineLimit),
		filter:            reg,
		tailLogs:          false,
		tailFlushInterval: flushInterval,
	}, nil
}

func (r *QueryResponse) AddEntry(logLine *logpipeline.LogLine) {
	for i := range r.Streams {
		if logLine.Labels.String() == r.Streams[i].Labels {
			r.Streams[i].AddEntry(logLine)
			return
		}
	}
	log.Debug("Adding new stream: ", logLine.Labels.String())
	newStream := QueryResponseStream{
		Labels:  logLine.Labels.String(),
		Entries: []QueryResponseLog{},
	}
	newStream.AddEntry(logLine)
	r.Streams = append(r.Streams, newStream)
}

func NewLokiApiInRouter(r *mux.Router, k8sMgr k8smanager.K8sManager) *mux.Router {
	lokiApi := NewLokiApi(k8sMgr)
	r.HandleFunc("/prom/label", lokiApi.getLabels)
	r.HandleFunc("/prom/label/{label}/values", lokiApi.getLabelValues)
	r.HandleFunc("/prom/query", lokiApi.query)
	r.HandleFunc("/prom/tail", lokiApi.tail)
	return r
}

func NewLokiApi(k8sMgr k8smanager.K8sManager) LokiApi {
	return LokiApi{
		k8sManager: k8sMgr,
	}
}

func writeJsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Error(err)
	}
}

type LokiApi struct {
	k8sManager k8smanager.K8sManager
}

func (a *LokiApi) getLabels(w http.ResponseWriter, r *http.Request) {
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

func (a *LokiApi) getLabelValues(w http.ResponseWriter, r *http.Request) {
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

func convertMatcher(m *labels.Matcher) tsdblabels.Matcher {
	switch m.Type {
	case labels.MatchEqual:
		return tsdblabels.NewEqualMatcher(m.Name, m.Value)

	case labels.MatchNotEqual:
		return tsdblabels.Not(tsdblabels.NewEqualMatcher(m.Name, m.Value))

	case labels.MatchRegexp:
		res, err := tsdblabels.NewRegexpMatcher(m.Name, "^(?:"+m.Value+")$")
		if err != nil {
			panic(err)
		}
		return res

	case labels.MatchNotRegexp:
		res, err := tsdblabels.NewRegexpMatcher(m.Name, "^(?:"+m.Value+")$")
		if err != nil {
			panic(err)
		}
		return tsdblabels.Not(res)
	}
	panic("storage.convertMatcher: invalid matcher type")
}

func (a *LokiApi) query(w http.ResponseWriter, r *http.Request) {

	queryOpts, err := NewLogQueryOptsFromUrl(r.URL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx := r.Context()

	containerLogsOptsToFetch, err := a.k8sManager.GetLogsFetchOptions(ctx, queryOpts)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	logChannel, errorChannel := logpipeline.FetchContainersLogs(ctx, containerLogsOptsToFetch, queryOpts)

	response := QueryResponse{
		Streams: []QueryResponseStream{},
	}

	for line := range logChannel {
		select {
		case <-r.Context().Done():
			return
		case err, ok := <-errorChannel:
			if ok {
				log.Error("error: ", err)
			}
		default:
		}
		response.AddEntry(line)
	}

	log.Debug("writing response")

	// TODO switch to protobuff
	writeJsonResponse(w, response)
}

// TailHandler is a http.HandlerFunc for handling tail queries.
func (a *LokiApi) tail(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{
		//CheckOrigin: func(r *http.Request) bool { return true },
	}

	queryOpts, err := NewLogQueryOptsFromUrl(r.URL)
	if err != nil {
		server.WriteError(w, err)
		return
	}

	queryOpts.tailLogs = true
	// TODO temporary workaround should be done somehow nicely. Maybe split the time filter into two?
	queryOpts.to = time.Now().Add(time.Hour * 24)

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error("Error in upgrading websocket ", fmt.Sprintf("%v", err))
		return
	}
	defer func() {
		err := conn.Close()
		log.Error("Error closing websocket ", fmt.Sprintf("%v", err))
	}()

	ctx := r.Context()
	containerLogsOptsToFetch, err := a.k8sManager.GetLogsFetchOptions(ctx, queryOpts)
	if err != nil {
		server.WriteError(w, err)
		return
	}

	logChannel, errorChannel := logpipeline.FetchContainersLogs(ctx, containerLogsOptsToFetch, queryOpts)

	response := QueryResponse{
		Streams: []QueryResponseStream{},
	}

	lastUpdateTime := time.Time{}
	for line := range logChannel {
		select {
		case <-r.Context().Done():
			return
		case err, ok := <-errorChannel:
			if ok {
				log.Error("error: ", err)
			}
		default:
		}
		response.AddEntry(line)

		log.Debug("seconds since update: ", time.Since(lastUpdateTime).Seconds())
		if time.Since(lastUpdateTime).Seconds() > 1 {
			log.Debug("writing response")
			if err = conn.WriteJSON(response); err != nil {
				log.Error("Error writing to websocket", fmt.Sprintf("%v", err))
				if err := conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, err.Error())); err != nil {
					log.Error("Error writing close message to websocket", fmt.Sprintf("%v", err))
				}
				break
			}
			lastUpdateTime = time.Now()
			response = QueryResponse{
				Streams: []QueryResponseStream{},
			}
		}
	}

}
