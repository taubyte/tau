package build

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func TestWriteCompressedToOutput_ToFile(t *testing.T) {
	dir := t.TempDir()
	outPath := dir + "/out.wasm"
	r := bytes.NewReader([]byte("wasm content"))
	path, err := writeCompressedToOutput(r, outPath, "tau-*.wasm")
	assert.NilError(t, err)
	assert.Equal(t, path, outPath)
	data, err := os.ReadFile(outPath)
	assert.NilError(t, err)
	assert.DeepEqual(t, data, []byte("wasm content"))
}

func TestWriteCompressedToOutput_ToTempPrintsPath(t *testing.T) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	rdr := bytes.NewReader([]byte("data"))
	path, err := writeCompressedToOutput(rdr, "", "tau-build-*.wasm")
	assert.NilError(t, err)
	assert.Assert(t, path != "")
	assert.Assert(t, strings.HasSuffix(path, ".wasm"))
	w.Close()
	out, _ := io.ReadAll(r)
	assert.Assert(t, strings.TrimSpace(string(out)) == path, "stdout should be the temp path")
}
