package url

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	ma "github.com/multiformats/go-multiaddr"
	"gotest.tools/v3/assert"
)

func downloadFile(url string, w io.Writer) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	_, err = io.Copy(w, resp.Body)
	if err != nil {
		return err
	}

	return nil
}

func TestBackend(t *testing.T) {
	backend := New()
	assert.Equal(t, backend.Scheme(), "url")

	httpUrl := "/dns4/get.tau.link/https/path/tau"
	incorrectUris := []string{
		"/dns4/ping.examples.tau",
		"/file//tmp/test",
		"/file/tmp/test",
	}

	for _, uri := range incorrectUris {
		mAddr, err := ma.NewMultiaddr(uri)
		if err != nil {
			t.Error(err)
			return
		}

		if _, err := backend.Get(mAddr); err == nil {
			t.Error("expected error")
		}
	}

	// Missing Coverage: Not sure how to get error for read all on successful http get without adding a mock http client
	mAddr, err := ma.NewMultiaddr(httpUrl)
	assert.NilError(t, err)

	httpReader, err := backend.Get(mAddr)
	assert.NilError(t, err)

	data, err := io.ReadAll(httpReader)
	assert.NilError(t, err)

	var expData bytes.Buffer
	err = downloadFile("https://get.tau.link/tau", &expData)
	assert.NilError(t, err)

	assert.DeepEqual(t, data, expData.Bytes())

	if err = backend.Close(); err != nil {
		t.Error(err)
	}
}
