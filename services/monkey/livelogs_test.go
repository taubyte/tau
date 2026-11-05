package monkey

import (
	"context"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/taubyte/tau/p2p/peer"
	protocolCommon "github.com/taubyte/tau/services/common"
)

func TestStreamLogs_PublishesWhileTheJobWrites(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	node := peer.Mock(ctx)
	defer node.Close()

	var lock sync.Mutex
	var chunks []protocolCommon.LiveLogChunk
	err := node.PubSubSubscribe(protocolCommon.LiveLogsTopic, func(msg *pubsub.Message) {
		var c protocolCommon.LiveLogChunk
		if cbor.Unmarshal(msg.Data, &c) == nil {
			lock.Lock()
			chunks = append(chunks, c)
			lock.Unlock()
		}
	}, func(error) {})
	if err != nil {
		t.Fatal(err)
	}

	logFile, err := os.CreateTemp(t.TempDir(), "log-")
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()

	m := &Monkey{Id: "job1", Service: &Service{ctx: ctx, node: node}, logFile: logFile}
	stop := m.streamLogs()

	want := ""
	seenBeforeEnd := 0
	for i := 0; i < 5; i++ {
		line := fmt.Sprintf("step %d\n", i)
		fmt.Fprint(logFile, line)
		want += line
		time.Sleep(3 * liveLogsInterval)

		lock.Lock()
		seenBeforeEnd = len(chunks)
		lock.Unlock()
	}
	if seenBeforeEnd < 3 {
		t.Fatalf("only %d chunks published while the job was writing", seenBeforeEnd)
	}

	// the job seeks around its own handle; the stream must not care
	logFile.Seek(0, 0)
	stop()

	// pubsub delivery is asynchronous
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		lock.Lock()
		ended := len(chunks) > 0 && chunks[len(chunks)-1].Done
		lock.Unlock()
		if ended {
			break
		}
	}

	lock.Lock()
	defer lock.Unlock()
	sort.SliceStable(chunks, func(i, j int) bool { return chunks[i].Offset < chunks[j].Offset })

	got := ""
	for _, c := range chunks {
		if c.Job != "job1" {
			t.Fatalf("chunk for job %q", c.Job)
		}
		if c.Offset != int64(len(got)) {
			t.Fatalf("chunk at %d, log is %d long", c.Offset, len(got))
		}
		got += string(c.Data)
	}
	if got != want {
		t.Fatalf("log streamed as %q, want %q", got, want)
	}
	if last := chunks[len(chunks)-1]; !last.Done || last.Offset != int64(len(want)) {
		t.Fatalf("last chunk %+v is not the end of the log", last)
	}
}
