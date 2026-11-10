package monkey

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

	id := make([]byte, 8)
	rand.Read(id)
	run := hex.EncodeToString(id)

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
				m.publishLogChunk(protocolCommon.LiveLogChunk{Job: m.Id, Run: run, Offset: offset, Data: buf[:n]})
				offset += int64(n)
			}
		}

		ticker := time.NewTicker(liveLogsInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				flush()
				m.publishLogChunk(protocolCommon.LiveLogChunk{Job: m.Id, Run: run, Offset: offset, Done: true})
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

	// Patrick takes a job's log only from the peer the job was assigned to,
	// and the job was taken through the client node, so the log goes out
	// through it too. Sent from the main node, every chunk of a node running
	// with a client node was refused.
	node := m.Service.clientNode
	if node == nil {
		node = m.Service.node
	}

	// the job's own context may already be cancelled; the last chunks still go out
	ctx, cancel := context.WithTimeout(node.Context(), 5*time.Second)
	defer cancel()
	if err = node.PubSubPublish(ctx, protocolCommon.LiveLogsTopic, data); err != nil {
		logger.Debugf("Publishing log of job `%s` failed: %s", m.Id, err.Error())
	}
}
