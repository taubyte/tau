package decompile

import (
	"testing"

	"github.com/spf13/afero"
	"gotest.tools/v3/assert"
)

func TestWriteInlined(t *testing.T) {
	// no filesystem to write to, nothing to do
	assert.NilError(t, (&Decompiler{}).writeInlined(map[string][]string{"a": {"x"}}))

	fs := afero.NewMemMapFs()
	d := &Decompiler{files: afero.NewBasePathFs(fs, "/out")}
	assert.NilError(t, d.writeInlined(map[string][]string{
		"dns/example.com.zone": {"; head", "", "@ IN MX 10 mail."},
		"empty.zone":           {},
	}))
	body, err := afero.ReadFile(fs, "/out/dns/example.com.zone")
	assert.NilError(t, err)
	assert.Equal(t, string(body), "; head\n\n@ IN MX 10 mail.\n")
	body, err = afero.ReadFile(fs, "/out/empty.zone")
	assert.NilError(t, err)
	assert.Equal(t, string(body), "")

	// rooted: a path in an object from outside cannot write beside the tree
	assert.Assert(t, d.writeInlined(map[string][]string{"../escape": {"x"}}) != nil)

	ro := &Decompiler{files: afero.NewReadOnlyFs(afero.NewMemMapFs())}
	assert.ErrorContains(t, ro.writeInlined(map[string][]string{"dns/z": {"x"}}), "writing dns/z")
}
