package logpipeline

import (
	"context"
	"sort"
	"time"

	log "github.com/sirupsen/logrus"
)

func Sort(ctx context.Context, streamConf LogQueryOpts, in <-chan *LogLine) <-chan *LogLine {
	out := make(chan *LogLine)

	go func() {
		defer close(out)
		var linesBuffer []*LogLine
		bufferSize := 0
		lastBufferFlush := time.Now().Add(-time.Second * 5)
		for line := range in {
			// Append lines to the buffer so we can sort them
			linesBuffer = append(linesBuffer, line)
			bufferSize++
			if streamConf.TailLogs() && time.Since(lastBufferFlush) > streamConf.TailFlushInterval() {
				// TODO avoid duplication of code below
				// Sort the line buffer by timestamp
				sort.Slice(linesBuffer, func(i, j int) bool {
					return linesBuffer[i].Timestamp.After(linesBuffer[j].Timestamp.GetTime())
				})
				// Push the lines to the out channel
				for _, inOrderLine := range linesBuffer {
					select {
					case <-ctx.Done():
						return
					case out <- inOrderLine:
					}
				}
				// Flush the buffer and reset the size counter.
				linesBuffer = []*LogLine{}
				lastBufferFlush = time.Now()
			}
			select {
			case <-ctx.Done():
				return
			default:
				continue
			}
		}
		// Flush the rest of logs in buffer
		if bufferSize > 0 {
			sort.Slice(linesBuffer, func(i, j int) bool {
				return linesBuffer[i].Timestamp.After(linesBuffer[j].Timestamp.GetTime())
			})
			for _, inOrderLine := range linesBuffer {
				select {
				case <-ctx.Done():
					return
				case out <- inOrderLine:
				}
			}
		}
		log.Debug("finished sorting")
	}()
	return out
}
