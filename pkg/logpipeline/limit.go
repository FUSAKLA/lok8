package logpipeline

import (
	"context"

	log "github.com/sirupsen/logrus"
)

func LimitLinesCount(ctx context.Context, closeFunc context.CancelFunc, lineLimit int, in <-chan *LogLine) <-chan *LogLine {
	out := make(chan *LogLine)
	go func() {
		defer close(out)
		lineCount := 0
		for line := range in {
			select {
			case out <- line:
				lineCount++
			case <-ctx.Done():
				return
			}
			if lineCount >= lineLimit {
				log.Debug("line limit exceeded")
				closeFunc()
				return
			}
		}
		log.Debug("finished line limit filtering")
	}()
	return out
}
