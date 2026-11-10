package jobs

import (
	"bytes"
	"io"
	"testing"
	"time"
)

type nopModule struct{ *bytes.Reader }

func (nopModule) Close() error { return nil }

// While one op's module is being stashed, the next op can build: the slot is
// held for the build only.
func TestStashDoesNotHoldBuildSlot(t *testing.T) {
	slots := make(chan struct{}, 1)
	stashing := make(chan struct{})
	release := make(chan struct{})

	go buildThenStash(slots,
		func() (io.ReadSeekCloser, error) { return nopModule{bytes.NewReader(nil)}, nil },
		func(io.ReadSeekCloser) (string, error) {
			close(stashing)
			<-release
			return "cid", nil
		})
	<-stashing
	defer close(release)

	built := make(chan struct{})
	go buildThenStash(slots,
		func() (io.ReadSeekCloser, error) { close(built); return nopModule{bytes.NewReader(nil)}, nil },
		func(io.ReadSeekCloser) (string, error) { return "cid", nil })

	select {
	case <-built:
	case <-time.After(2 * time.Second):
		t.Fatal("second build waited for the first one's stash")
	}
}
