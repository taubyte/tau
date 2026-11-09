package pebbleds

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/cockroachdb/pebble/v2"
	ds "github.com/ipfs/go-datastore"
)

// writerDir is set in the child a kill test starts: the store it writes to.
const writerDir = "PEBBLEDS_TEST_WRITER_DIR"

var outlived = ds.NewKey("/outlived")

// A WRITE OUTLIVES ITS PROCESS once syncWithin has passed, with nobody asking for a
// sync. Pebble keeps a write that asks for none in its own buffer until a log block
// fills, and a process that dies takes it, however long ago it was made.
func TestAWriteOutlivesItsProcessWithinTheBound(t *testing.T) {
	if dir := os.Getenv(writerDir); dir != "" {
		writeAndWait(dir)
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestAWriteOutlivesItsProcessWithinTheBound$")
	cmd.Env = append(os.Environ(), writerDir+"="+dir)
	out, err := cmd.StdoutPipe()
	must(t, err)
	must(t, cmd.Start())
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || line != "written\n" {
		cmd.Process.Kill()
		t.Fatalf("the writer said %q: %v", line, err)
	}
	time.Sleep(5 * syncWithin)
	must(t, cmd.Process.Kill())
	// It was killed, which is what Wait reports.
	cmd.Wait()

	s, err := Open(dir)
	must(t, err)
	defer s.Close()
	v, err := s.Get(t.Context(), outlived)
	if err != nil || string(v) != "v" {
		t.Fatalf("the write died with its process: %q, %v", v, err)
	}
}

// writeAndWait is the child: it writes one key, says so, and waits to be killed.
func writeAndWait(dir string) {
	s, err := Open(dir)
	if err == nil {
		err = s.Put(context.Background(), outlived, []byte("v"))
	}
	if err != nil {
		fmt.Println("err", err)
		os.Exit(1)
	}
	fmt.Println("written")
	for {
		time.Sleep(time.Hour)
	}
}

// A WRITE ARMS ONE SYNC, which an explicit Sync since covers; a store whose writes
// sync themselves arms none; and one closed with a sync armed closes, the sync
// finding it closed if it fires.
func TestAWriteArmsOneSync(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	must(t, s.Put(ctx, outlived, []byte("v")))
	if !s.dirty.Load() {
		t.Fatal("a write no sync covers armed none")
	}
	must(t, s.Sync(ctx, ds.Key{}))
	if s.dirty.Load() {
		t.Fatal("a sync left the write it covered armed")
	}
	s.flushed()

	must(t, s.Delete(ctx, outlived))
	must(t, s.Close())
	s.flushed()

	synced := open(t, WithWriteOptions(pebble.Sync))
	must(t, synced.Put(ctx, outlived, []byte("v")))
	if synced.dirty.Load() {
		t.Fatal("a write that synced itself armed a sync")
	}
}
