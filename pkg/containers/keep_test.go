package containers

import (
	"context"
	"testing"
	"time"
)

// A kept image is never removed, however long it sits idle. The client here
// has no backend, so reaching Remove at all would panic.
func TestCleanUnusedKeepsBaseImages(t *testing.T) {
	name := "example/keep-base:test"
	lastUsed.Store(name, time.Now().Add(-48*time.Hour))
	defer lastUsed.Delete(name)

	if err := (&Client{}).CleanUnused(context.Background(), time.Hour, name); err != nil {
		t.Fatal(err)
	}
	if _, ok := lastUsed.Load(name); !ok {
		t.Fatal("kept image was dropped from tracking")
	}
}
