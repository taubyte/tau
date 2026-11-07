package kvdb

import (
	"testing"

	ds "github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// Reopening a store is routine: once its format is current, an open and close
// logs nothing at Info or above.
func TestOpenCloseLogsNoRoutineInfo(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	store := dssync.MutexWrap(ds.NewMapDatastore())
	for i := 0; i < 3; i++ {
		opts := DefaultOptions()
		opts.Logger = zap.New(core).Sugar()
		d, err := NewDatastore(store, ds.NewKey("loglevel"), newTestDagsync(), nil, opts)
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Close(); err != nil {
			t.Fatal(err)
		}
		infos := logs.TakeAll()
		if i == 0 {
			continue // fresh store: the v0 -> v2 migration ran and may say so
		}
		n := 0
		for _, e := range infos {
			if e.Level >= zapcore.InfoLevel {
				n++
			}
		}
		if n != 0 {
			t.Fatalf("reopen %d: %d entries at Info or above, want 0: %v", i, n, infos)
		}
	}
}
