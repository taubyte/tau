package uri

import (
	"io/ioutil"
	"net/http"
	"strings"
	"testing"
)

type dummyRoundTripper struct{}

func (d *dummyRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{Status: "dummy", Body: ioutil.NopCloser(strings.NewReader("body dummy"))}, nil
}

func TestNew(t *testing.T) {
	_, err := New()
	if err != nil {
		t.Error(err)
	}
}

func TestWithHTTPClient(t *testing.T) {
	c, err := New(WithHTTPClient(&http.Client{Transport: &dummyRoundTripper{}}))
	if err != nil {
		t.Error(err)
	}

	resp, _ := c.httpClient.Get("test")
	if resp.Status != "dummy" {
		t.Errorf("Expected status dummy, got %s", resp.Status)
	}
}

func TestOpen_File(t *testing.T) {
	o, err := Open("./open.go")
	if err != nil {
		t.Error(err)
	}

	b, err := ioutil.ReadAll(o)
	if err != nil {
		t.Error(err)
	}

	if !strings.HasPrefix(string(b), "package uri") {
		t.Errorf("Expected open file, go %s", string(b))
	}
}

func TestOpen_URL(t *testing.T) {
	tests := []struct {
		url string
	}{
		{"http://example.com"},
		{"https://example.com"},
	}

	for _, test := range tests {
		o, err := Open(test.url, WithHTTPClient(&http.Client{Transport: &dummyRoundTripper{}}))
		if err != nil {
			t.Error(err)
		}

		b, err := ioutil.ReadAll(o)
		if err != nil {
			t.Error(err)
		}

		if !strings.HasPrefix(string(b), "body dummy") {
			t.Errorf("Expected open file, go %s", string(b))
		}
	}
}
