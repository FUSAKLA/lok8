package logpipeline

import (
	"context"
	"sync"

	log "github.com/sirupsen/logrus"
)

func Merge(ctx context.Context, streams ...<-chan *LogLine) <-chan *LogLine {
	var wg sync.WaitGroup
	out := make(chan *LogLine, len(streams))

	output := func(stream <-chan *LogLine, o chan<- *LogLine) {
		wg.Add(1)
		defer wg.Done()
		for line := range stream {
			select {
			case <-ctx.Done():
				return
			case o <- line:
			}
		}
	}

	for _, c := range streams {
		go output(c, out)
	}

	go func() {
		defer log.Debug("waiting for all logs to merge")
		wg.Wait()
		log.Debug("done merging log channels")
		close(out)
	}()
	return out
}
