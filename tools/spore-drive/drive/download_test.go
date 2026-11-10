package drive

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/h2non/filetype"
	"gotest.tools/v3/assert"
)

func TestDownload(t *testing.T) {
	elf := append([]byte{0x7f, 0x45, 0x4c, 0x46}, make([]byte, 1024)...)
	r := serveRelease(t, elf)

	version, err := r.latestVersion()
	assert.NilError(t, err)
	assert.Equal(t, version, "1.2.3")

	tauBin, err := r.tau(version, "x86_64")
	assert.NilError(t, err)
	kind, err := filetype.Match(tauBin[:512])
	assert.NilError(t, err)
	assert.Equal(t, kind.Extension, "elf")
}

// A RELEASE THAT NAMES NO VERSION is refused, not indexed into.
func TestALatestReleaseWithNoVersionIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"tag_name":""}`))
	}))
	t.Cleanup(srv.Close)

	_, err := releases{latest: srv.URL}.latestVersion()
	assert.ErrorContains(t, err, "names no version")
	assert.Equal(t, normalizeVersion(""), "")
	assert.Equal(t, normalizeVersion("v1.2.3"), "1.2.3")
}
