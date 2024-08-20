package embed

import (
	"archive/zip"
	"bytes"
	"io"
	"testing"

	"github.com/spf13/afero/zipfs"
	"go4.org/readerutil"
	"gotest.tools/v3/assert"

	_ "embed"
	"fmt"
)

func TestEmbed(t *testing.T) {
	zipReader, err := zip.NewReader(
		readerutil.NewBufferingReaderAt(bytes.NewBuffer(runtimesData)),
		int64(len(runtimesData)),
	)
	assert.NilError(t, err)

	amd64, err := RuntimeADM64()
	assert.NilError(t, err)
	riscv64, err := RuntimeRISCV64()
	assert.NilError(t, err)

	fs := zipfs.New(zipReader)
	for name, data := range map[string][]byte{"amd64.wasm": amd64, "riscv64.wasm": riscv64} {
		t.Run(fmt.Sprintf("Runtime %s match", name), func(t *testing.T) {
			f, err := fs.Open(name)
			assert.NilError(t, err)

			fBytes, err := io.ReadAll(f)
			assert.NilError(t, err)

			assert.Equal(t, bytes.Compare(fBytes, data), 0)
		})
	}
}
