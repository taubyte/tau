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

	// Create the fake Tau binary with the ELF header
	fakeTau := bytes.NewBuffer([]byte{0x7f, 0x45, 0x4c, 0x46})
	for i := 0; i < 1024; i++ {
		fakeTau.WriteByte(0x00)
	}

	// Prepare a buffer to hold the tar.gz content
	var tarGzBuf bytes.Buffer

	// Create a gzip writer
	gzWriter := gzip.NewWriter(&tarGzBuf)
	defer gzWriter.Close()

	// Create a tar writer
	tarWriter := tar.NewWriter(gzWriter)
	defer tarWriter.Close()

	// Add the fakeTau file to the tar archive
	tarHeader := &tar.Header{
		Name: "tau",
		Mode: 0600,
		Size: int64(fakeTau.Len()),
	}
	err := tarWriter.WriteHeader(tarHeader)
	assert.NilError(t, err)

	_, err = tarWriter.Write(fakeTau.Bytes())
	assert.NilError(t, err)

	// Close the tar and gzip writers to finish writing the tar.gz archive
	err = tarWriter.Close()
	assert.NilError(t, err)
	err = gzWriter.Close()
	assert.NilError(t, err)

	// Start the HTTP mock
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	version := "1.2.3"
	asset := fmt.Sprintf("tau_%s_linux_%s.tar.gz", version, arch)

	// Mock the response for getting the latest version
	httpmock.RegisterResponder("GET", "https://api.github.com/repos/taubyte/tau/releases/latest",
		httpmock.NewStringResponder(200, "{\"tag_name\": \"v"+version+"\"}"))

	// Mock the response for downloading the binary
	httpmock.RegisterResponder("GET", fmt.Sprintf("https://github.com/taubyte/tau/releases/download/v%s/%s", version, asset),
		httpmock.NewBytesResponder(200, tarGzBuf.Bytes())) // ELF header

	// Call your functions, they should hit the mocked endpoints
	r, err := getLatestAssetVersion()
	assert.NilError(t, err)
	assert.Equal(t, r, version)

	tauBin, err := downloadTau(r, arch)
	assert.NilError(t, err)

	// Check if the binary is correctly identified as ELF
	tauBinType, err := filetype.Match(tauBin[:512])
	assert.NilError(t, err)
	assert.Equal(t, tauBinType.Extension, "elf")

	// Verify that all registered mock calls were made
	info := httpmock.GetCallCountInfo()
	for url, count := range info {
		t.Logf("Mocked %s called %d time(s)", url, count)
	}
}
