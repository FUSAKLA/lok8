package logpipeline

import (
	"context"
	"regexp"
	"time"

	"github.com/fusakla/lok8/pkg/k8smanager"
)

type LogQueryOpts interface {
	From() time.Time
	To() time.Time
	LinesLimit() int
	Filter() *regexp.Regexp
	TailLogs() bool
	TailFlushInterval() time.Duration
}

func FetchContainersLogs(ctx context.Context, containerLogsOptsToFetch []k8smanager.ContainerLogsOpts,
	queryOpts LogQueryOpts) (<-chan *LogLine, <-chan error) {
	var (
		logStreamChannels     []<-chan *LogLine
		pipelineErrorChannels []<-chan error
		logChan               <-chan *LogLine
		errChan               <-chan error
	)
	logsCtx, cancelFunc := context.WithCancel(ctx)

	// Start fetching logs for all discovered containers.
	for _, opts := range containerLogsOptsToFetch {
		lChan, eChan := Fetch(logsCtx, opts.LogResponseWrapper, opts.Labels)
		logStreamChannels = append(logStreamChannels, lChan)
		pipelineErrorChannels = append(pipelineErrorChannels, eChan)
	}

	// Merge all channels from separate containers to one.
	mergedChan := Merge(logsCtx, logStreamChannels...)

	sortedChan := Sort(ctx, queryOpts, mergedChan)

	// TODO Time range filtering should allow disabling filter for newer lines for tailing
	// Drop log lines out of queried time range
	timeRangeFilteredChan := FilterTimeRange(logsCtx, queryOpts.From(), queryOpts.To(), sortedChan)

	// Filter the requests by regex filer from query.
	regexFilteredChan := FilterRegex(logsCtx, queryOpts.Filter(), timeRangeFilteredChan)

	if queryOpts.TailLogs() {
		logChan = regexFilteredChan
	} else {
		// Limit total number of returned log lines by limit in query
		logChan = LimitLinesCount(logsCtx, cancelFunc, queryOpts.LinesLimit(), regexFilteredChan)
	}
	errChan = MergeErrors(pipelineErrorChannels...)

	return logChan, errChan
}
