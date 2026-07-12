//go:build dreaming

package benchmarks

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	hoarderClient "github.com/taubyte/tau/clients/p2p/hoarder"
	db "github.com/taubyte/tau/core/services/substrate/components/database"
	storageIface "github.com/taubyte/tau/core/services/substrate/components/storage"
	dbsvc "github.com/taubyte/tau/services/substrate/components/database"
	storages "github.com/taubyte/tau/services/substrate/components/storage"
)

var (
	dataOnce sync.Once
	dataErr  error

	dbService      db.Service
	storageService storageIface.Service
)

// bootData lazily meshes the shared universe's substrate node to the hoarder
// (in production the pnet swarm links them for the data plane; dream needs
// an explicit Mesh() call) and constructs the database/storage service
// handles used by the benchmarks below. Shared by BenchmarkDatabase* and
// BenchmarkStorage* so the mesh + connectedness poll only happens once.
func bootData(b *testing.B) (db.Service, storageIface.Service) {
	b.Helper()
	u := sharedUniverse(b)

	dataOnce.Do(func() {
		u.Mesh()

		hcli, err := hoarderClient.New(u.Context(), u.Substrate().Node())
		if err != nil {
			dataErr = err
			return
		}

		discovered := false
		for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
			if u.Substrate().Node().Peer().Network().Connectedness(u.Hoarder().Node().ID()) == network.Connected {
				discovered = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !discovered {
			dataErr = fmt.Errorf("substrate node did not discover the hoarder in time")
			return
		}

		dsrv, err := dbsvc.New(u.Substrate(), hcli)
		if err != nil {
			dataErr = fmt.Errorf("creating database service failed with: %w", err)
			return
		}
		dbService = dsrv

		ssrv, err := storages.New(u.Substrate(), hcli)
		if err != nil {
			dataErr = fmt.Errorf("creating storage service failed with: %w", err)
			return
		}
		storageService = ssrv
	})
	if dataErr != nil {
		b.Fatal(dataErr)
	}

	return dbService, storageService
}

// benchDatabaseKV resolves the shared benchdb database's KV handle, polling
// since the config published by bootShared's injectProject takes a moment to
// propagate through TNS to the substrate's view.
func benchDatabaseKV(b *testing.B) db.KV {
	b.Helper()
	dsrv, _ := bootData(b)

	ctx := db.Context{
		ProjectId: testProjectId,
		Matcher:   databaseMatch,
	}

	var (
		database db.Database
		err      error
	)
	for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); {
		database, err = dsrv.Database(ctx)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		b.Fatal(err)
	}

	return database.KV()
}

// BenchmarkDatabasePut measures the remote hoarder-backed KV write path.
func BenchmarkDatabasePut(b *testing.B) {
	u := sharedUniverse(b)
	kv := benchDatabaseKV(b)
	ctx := u.Context()
	value := bytes.Repeat([]byte("d"), 1024)

	b.ReportAllocs()
	n := 0
	for b.Loop() {
		// strconv.Itoa (not fmt.Sprintf) so unique-key generation stays a
		// negligible slice of the timed write path rather than a reflection-
		// backed hot spot.
		key := "put-bench/" + strconv.Itoa(n)
		n++
		if err := kv.Put(ctx, key, value); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDatabaseGet measures the remote hoarder-backed KV read path.
func BenchmarkDatabaseGet(b *testing.B) {
	u := sharedUniverse(b)
	kv := benchDatabaseKV(b)
	ctx := u.Context()

	value := bytes.Repeat([]byte("g"), 1024)
	key := "get-bench/key"
	if err := kv.Put(ctx, key, value); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	for b.Loop() {
		got, err := kv.Get(ctx, key)
		if err != nil {
			b.Fatal(err)
		}
		// full content check (cheap memcmp of 1KiB) so a truncated or wrong
		// value fails the benchmark instead of silently mismeasuring the path.
		if !bytes.Equal(got, value) {
			b.Fatalf("read mismatch: expected %d bytes got %d", len(value), len(got))
		}
	}
}

// storageBlob is a fixed 256KiB deterministic payload for the storage
// benchmarks below.
var storageBlob = func() []byte {
	blob := make([]byte, 256*1024)
	for i := range blob {
		blob[i] = byte(i)
	}
	return blob
}()

// BenchmarkStorageAdd measures the raw blob-add path. Add/GetFile operate
// directly against the node's blockstore with no project config or TNS
// lookup involved (see services/substrate/components/storage/methods.go).
func BenchmarkStorageAdd(b *testing.B) {
	_, stsrv := bootData(b)

	b.ReportAllocs()
	for b.Loop() {
		c, err := stsrv.Add(bytes.NewReader(storageBlob))
		if err != nil {
			b.Fatal(err)
		}
		// fail fast if Add ever returns a nil/undefined cid with no error,
		// which would mean we're timing a no-op rather than the add path.
		if !c.Defined() {
			b.Fatal("Add returned an undefined cid")
		}
	}
}

// BenchmarkStorageGet measures the raw blob-fetch path.
func BenchmarkStorageGet(b *testing.B) {
	u := sharedUniverse(b)
	_, stsrv := bootData(b)

	cid, err := stsrv.Add(bytes.NewReader(storageBlob))
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	for b.Loop() {
		file, err := stsrv.GetFile(u.Context(), cid)
		if err != nil {
			b.Fatal(err)
		}
		n, err := io.Copy(io.Discard, file)
		if err != nil {
			b.Fatal(err)
		}
		file.Close()
		if n != int64(len(storageBlob)) {
			b.Fatalf("expected %d bytes got %d", len(storageBlob), n)
		}
	}
}
