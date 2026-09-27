package schema_test

import (
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/interp/decompile"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

// fixtureOver is the basic fixture with writes landing in memory, so a test can change
// the zone file without touching the tree.
func fixtureOver() afero.Fs {
	return afero.NewCopyOnWriteFs(
		afero.NewReadOnlyFs(afero.NewBasePathFs(afero.NewOsFs(), "../fixtures/basic")),
		afero.NewMemMapFs(),
	)
}

// A file written on Windows is the same zone: a line is a line, not a line and a \r
// that the zone parser would then read as part of the record.
func TestZoneFileLinesAreLines(t *testing.T) {
	fs := fixtureOver()
	assert.NilError(t, afero.WriteFile(fs, "/dns/example.com.zone",
		[]byte("@ 3600 IN MX 10 mail.example.com.\r\nvpn 300 IN A 203.0.113.9\r\n"), 0o644))

	zone := only(t, resources(t, compileFor(t, schema.WithVirtual(fs, "/")).Flat(), "zone"))
	assert.DeepEqual(t, zone["records"], []any{
		"@ 3600 IN MX 10 mail.example.com.",
		"vpn 300 IN A 203.0.113.9",
	})
}

// A FILE THE COMPILE CANNOT SEE IS NOT A REFUSAL -- it is the mount protocol's case --
// and it must not be invented on the way back either: a decompile that wrote an empty
// file for it would turn "not fetched yet" into "a zone with no records".
func TestUnreachableZoneFileIsNeitherRefusedNorInvented(t *testing.T) {
	// Pointed elsewhere rather than deleted: a copy-on-write layer cannot remove what
	// only its read-only base holds.
	fs := fixtureOver()
	doc, err := afero.ReadFile(fs, "/zone/example_com.yaml")
	assert.NilError(t, err)
	doc = []byte(strings.Replace(string(doc), "file: dns/example.com.zone", "file: dns/elsewhere.zone", 1))
	assert.NilError(t, afero.WriteFile(fs, "/zone/example_com.yaml", doc, 0o644))

	obj := compileFor(t, schema.WithVirtual(fs, "/"))
	zone := only(t, resources(t, obj.Flat(), "zone"))
	_, has := zone["records"]
	assert.Assert(t, !has, "records were produced from a file that is not there: %v", zone["records"])

	out := decompileTo(t, obj)
	_, err = out.Stat("/dns/elsewhere.zone")
	assert.Assert(t, err != nil, "the decompile invented the file the compile never read")
}

// A tree that cannot be written is an error, not a decompile that reports success
// having written nothing.
func TestDecompileIntoReadOnlyTreeFails(t *testing.T) {
	d, err := schema.NewDecompiler(decompile.WithVirtual(afero.NewReadOnlyFs(afero.NewMemMapFs()), "/"))
	assert.NilError(t, err)
	assert.Assert(t, d.Decompile(compileFor(t, schema.WithLocal("../fixtures/basic"))) != nil)
}
