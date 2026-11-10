package drive

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/h2non/filetype"
	"github.com/jarcoal/httpmock"
	"gotest.tools/v3/assert"

	"archive/tar"
	"compress/gzip"
)

func TestDownload(t *testing.T) {
	arch := "amd64"

	fakeTau := bytes.NewBuffer([]byte{0x7f, 0x45, 0x4c, 0x46})
	for i := 0; i < 1024; i++ {
		fakeTau.WriteByte(0x00)
	}

	var tarGzBuf bytes.Buffer

	gzWriter := gzip.NewWriter(&tarGzBuf)
	defer gzWriter.Close()

	tarWriter := tar.NewWriter(gzWriter)
	defer tarWriter.Close()

	tarHeader := &tar.Header{
		Name: "taud",
		Mode: 0600,
		Size: int64(fakeTau.Len()),
	}
	err := tarWriter.WriteHeader(tarHeader)
	assert.NilError(t, err)

	_, err = tarWriter.Write(fakeTau.Bytes())
	assert.NilError(t, err)

	err = tarWriter.Close()
	assert.NilError(t, err)
	err = gzWriter.Close()
	assert.NilError(t, err)

	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	version := "1.2.3"
	asset := fmt.Sprintf("tau_%s_linux_%s.tar.gz", version, arch)

	httpmock.RegisterResponder("GET", "https://api.github.com/repos/taubyte/tau/releases/latest",
		httpmock.NewStringResponder(200, "{\"tag_name\": \"v"+version+"\"}"))

	httpmock.RegisterResponder("GET", fmt.Sprintf("https://github.com/taubyte/tau/releases/download/v%s/%s", version, asset),
		httpmock.NewBytesResponder(200, tarGzBuf.Bytes()))

	r, err := published.latestVersion()
	assert.NilError(t, err)
	assert.Equal(t, r, version)

	tauBin, err := published.tau(r, arch)
	assert.NilError(t, err)

	tauBinType, err := filetype.Match(tauBin[:512])
	assert.NilError(t, err)
	assert.Equal(t, tauBinType.Extension, "elf")

	info := httpmock.GetCallCountInfo()
	for url, count := range info {
		t.Logf("Mocked %s called %d time(s)", url, count)
	}
}
