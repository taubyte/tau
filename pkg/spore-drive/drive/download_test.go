package drive

import (
	"testing"

	"github.com/h2non/filetype"
	"gotest.tools/v3/assert"
)

func TestDownload(t *testing.T) {
	testProcessor := "x86_64"

	r, err := getLatestAssetVersion()
	assert.NilError(t, err)
	assert.Equal(t, r != "", true)

	tauBin, err := downloadTau(r, testProcessor)
	assert.NilError(t, err)

	tauBinType, err := filetype.Match(tauBin[:512])
	assert.NilError(t, err)
	assert.Equal(t, tauBinType.Extension, "elf")
}
