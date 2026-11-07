package migration

import (
	"bytes"
	"strings"
	"testing"

	"github.com/taubyte/tau/p2p/peer"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	mh "github.com/taubyte/tau/utils/multihash"
)

func TestMigrate_ThresholdStops(t *testing.T) {
	restoreN, restoreF := MaxDeleteBlocks, MaxDeleteFraction
	MaxDeleteBlocks, MaxDeleteFraction = 1, 0
	defer func() { MaxDeleteBlocks, MaxDeleteFraction = restoreN, restoreF }()

	m, _, tns := newTestMigrator(t)
	t.Cleanup(m.Close)
	ctx := t.Context()

	// A 3-block file (root + 2 chunks) in a legacy storage namespace.
	payload := bytes.Repeat([]byte("x"), 300<<10)
	cid, err := m.node.AddFile(bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	hash := mh.Hash("p1" + "" + "files")
	seedLegacy(t, m.node, hash, map[string][]byte{"file/data/1": []byte(cid)})
	tns.indexKeys = []string{"/projects/p1/x"}
	tns.storages["p1/"] = map[string]*structureSpec.Storage{"cfg": {Match: "files"}}

	keysBefore := countPrefix(t, m, crdtPrefix)
	blocksBefore := countPrefix(t, m, blocksPrefix)

	report := m.Migrate(ctx)
	if !strings.Contains(report.Err, "threshold") {
		t.Fatalf("report must carry the threshold error: %q", report.Err)
	}
	if report.SweptKeys != 0 || report.SweptBlocks != 0 {
		t.Fatalf("deleted past the threshold: %s", report.Summary())
	}
	if n := countPrefix(t, m, crdtPrefix); n != keysBefore {
		t.Fatalf("namespace keys deleted: %d -> %d", keysBefore, n)
	}
	if n := countPrefix(t, m, blocksPrefix); n != blocksBefore {
		t.Fatalf("blocks deleted: %d -> %d", blocksBefore, n)
	}
	if got, err := readFile(t, m.node, cid); err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("file lost: %v", err)
	}
	if rep := report.Instances[hash]; rep != nil && rep.Scrubbed {
		t.Fatalf("instance marked scrubbed: %+v", rep)
	}

	// Boot stops too: no drain retrying the same refusal every interval.
	if rep := m.Boot(); !strings.Contains(rep.Err, "threshold") {
		t.Fatalf("boot must hit the threshold: %q", rep.Err)
	}
	m.drainMu.Lock()
	on := m.drainOn
	m.drainMu.Unlock()
	if on {
		t.Fatal("drain armed after the threshold stopped the pass")
	}
	if n := countPrefix(t, m, blocksPrefix); n != blocksBefore {
		t.Fatalf("blocks deleted: %d -> %d", blocksBefore, n)
	}
}

func TestNew_MigrationDisabled(t *testing.T) {
	_, h, tns := newTestMigrator(t)
	node := peer.Mock(t.Context())
	hash := mh.Hash("p1" + "" + "users")
	seedLegacy(t, node, hash, map[string][]byte{"k": []byte("v")})
	tns.indexKeys = []string{"/projects/p1/x"}
	tns.dbs["p1/"] = map[string]*structureSpec.Database{"cfg": {Match: "users"}}

	m := New(t.Context(), node, h, tns, Disabled())
	t.Cleanup(m.Close)
	if rep := m.Boot(); !rep.Empty() {
		t.Fatalf("disabled migration ran: %s", rep.Summary())
	}
	if rep := m.Migrate(t.Context()); !rep.Empty() {
		t.Fatalf("disabled migration ran: %s", rep.Summary())
	}
	if n := countPrefix(t, m, crdtPrefix+hash+"/"); n == 0 {
		t.Fatal("disabled migration scrubbed a namespace")
	}
}
