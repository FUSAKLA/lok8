package logpipeline

import (
	"bufio"
	"context"
	"io"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/prometheus/tsdb/labels"
	log "github.com/sirupsen/logrus"
	"k8s.io/client-go/rest"
)

var InvalidLine = errors.New("InvalidLine")

type LogTimestamp struct {
	time.Time
}

func (t *LogTimestamp) GetTime() time.Time {
	return t.Time
}

func NewLogTimestampFromString(str string) (LogTimestamp, error) {
	t, err := time.Parse(time.RFC3339Nano, str)
	if err != nil {
		return LogTimestamp{}, err
	}
	return LogTimestamp{Time: t}, nil
}

type LogLabels map[string]string

func (l *LogLabels) String() string {
	return labels.FromMap(*l).String()
}

type LogLine struct {
	Labels    *LogLabels
	Timestamp LogTimestamp
	Log       string
}

func parseLogLine(lineText string) (*LogLine, error) {
	// Kubernetes adds timestamp in RFC3339Nano format to the beginning of the lines
	// separated by space so we parse it out.
	lineData := strings.SplitN(lineText, " ", 2)
	if len(lineData) != 2 {
		log.Error("failed to parse timestamp of log line: ", lineText)
		return nil, InvalidLine
	}
	ts, err := NewLogTimestampFromString(lineData[0])
	if err != nil {
		log.Error("invalid timestamp of log line: ", lineText)
		return nil, InvalidLine
	}
	return &LogLine{
		Labels:    nil,
		Timestamp: ts,
		Log:       lineData[1],
	}, nil
}

func readLine(reader *bufio.Reader) (*LogLine, error) {
	// Read next line
	line, err := reader.ReadString('\n')
	if err != nil {
		//log.Debug("failed to read streamed line: ", line, "  err: ", err)
		return nil, err
	}
	line = strings.TrimSuffix(line, "\n")
	l, err := parseLogLine(line)
	if err != nil {
		return nil, err
	}
	return l, nil
}

func Fetch(ctx context.Context, req rest.ResponseWrapper, labels LogLabels) (<-chan *LogLine, <-chan error) {
	outCh := make(chan *LogLine)
	errCh := make(chan error, 1)
	go func() {
		defer func() {
			log.Debug("finished reading logs for: ", labels)
			close(outCh)
			close(errCh)
		}()

		log.Debug("fetching logs for: ", labels)
		// Query logs from Kubernetes API
		readCloser, err := req.Stream()
		if err != nil {
			if !strings.HasPrefix(err.Error(), "previous terminated container") {
				log.Error("failed to query logs, error: ", err)
			}
			return
		}
		defer func() {
			if err := readCloser.Close(); err != nil {
				log.Error("failed to close reader. Error: ", err)
			}
		}()

		reader := bufio.NewReader(readCloser)

		log.Debug("started reading logs for: ", labels)

		// Parse the log line and pass it to the log channel with labels
		for {
			// TODO consider throttling when tailing. We get context canceled all the time.
			newLine, err := readLine(reader)
			// If we hit EOF it means we read all the logs and finish.
			if err == io.EOF {
				log.Debug("found EOF of  ", labels, " logs")
				if err := readCloser.Close(); err != nil {
					log.Error("failed to close reader. Error: ", err)
				}
				return
			}
			if err != nil {
				continue
			}

			// Add stream labels to the LogLine.
			newLine.Labels = &labels
			select {
			case outCh <- newLine:
			case <-ctx.Done():
				return
			}
		}
	}()
	return outCh, errCh
}
