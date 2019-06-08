package logpipeline

import (
	"context"
	"regexp"
	"time"

	log "github.com/sirupsen/logrus"
)

func FilterRegex(ctx context.Context, filter *regexp.Regexp, in <-chan *LogLine) <-chan *LogLine {
	out := make(chan *LogLine)
	go func() {
		defer close(out)
		for line := range in {
			if !filter.MatchString(line.Log) {
				continue
			}
			select {
			case out <- line:
			case <-ctx.Done():
				return
			}
		}
		log.Debug("finished regex filtering")
	}()
	return out
}

func FilterTimeRange(ctx context.Context, from, to time.Time, in <-chan *LogLine) <-chan *LogLine {
	out := make(chan *LogLine)
	go func() {
		defer close(out)
		for line := range in {
			if line.Timestamp.Before(from) || to.Before(line.Timestamp.GetTime()) {
				continue
			}
			select {
			case out <- line:
			case <-ctx.Done():
				return
			}
		}
		log.Debug("finished time range filtering")
	}()
	return out
}
