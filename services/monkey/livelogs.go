package monkey

import (
	"context"
	"os"
	"time"

	"github.com/fxamacker/cbor/v2"
	protocolCommon "github.com/taubyte/tau/services/common"
)

var (
	liveLogsInterval  = 250 * time.Millisecond
	liveLogsChunkSize = 64 * 1024
)

// streamLogs publishes the job's log on protocolCommon.LiveLogsTopic as it
// grows. It follows the file through its own handle, so the job's writes and
// seeks are untouched. The returned stop publishes what is left, then the
// final chunk, and waits for both.
func (m *Monkey) streamLogs() (stop func()) {
	ctx, cancel := context.WithCancel(m.Service.ctx)
	done := make(chan struct{})

	go func() {
		defer close(done)

		f, err := os.Open(m.logFile.Name())
		if err != nil {
			logger.Errorf("Following log of job `%s` failed: %s", m.Id, err.Error())
			return
		}
		defer f.Close()

		var offset int64
		buf := make([]byte, liveLogsChunkSize)
		flush := func() {
			for {
				n, _ := f.ReadAt(buf, offset)
				if n == 0 {
					return
				}
				m.publishLogChunk(protocolCommon.LiveLogChunk{Job: m.Id, Offset: offset, Data: buf[:n]})
				offset += int64(n)
			}
		}

		ticker := time.NewTicker(liveLogsInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				flush()
				m.publishLogChunk(protocolCommon.LiveLogChunk{Job: m.Id, Offset: offset, Done: true})
				return
			case <-ticker.C:
				flush()
			}
		}
	}()

	return func() {
		cancel()
		<-done
	}
}

func (m *Monkey) publishLogChunk(chunk protocolCommon.LiveLogChunk) {
	data, err := cbor.Marshal(chunk)
	if err != nil {
		return
	}

	// the job's own context may already be cancelled; the last chunks still go out
	ctx, cancel := context.WithTimeout(m.Service.node.Context(), 5*time.Second)
	defer cancel()
	if err = m.Service.node.PubSubPublish(ctx, protocolCommon.LiveLogsTopic, data); err != nil {
		logger.Debugf("Publishing log of job `%s` failed: %s", m.Id, err.Error())
	}
}
