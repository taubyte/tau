package follow

import (
	"bytes"
	"strings"
	"testing"

	"github.com/taubyte/tau/core/services/patrick"
	"gotest.tools/v3/assert"
)

func lineOf(s, node string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, node) {
			return l
		}
	}
	return ""
}

// Only the odd node is flagged; one node is compared with nothing; no padded row.
func TestWriteStatusNotes(t *testing.T) {
	var out bytes.Buffer
	WriteStatus(&out, Report{Repos: []RepoStatus{{Repo: "code", Status: "success"}}, Serving: []Served{
		{Resource: "function a", Node: "n1", Code: 200, Hash: "h1"},
		{Resource: "function a", Node: "n2", Code: 200, Hash: "h1"},
		{Resource: "function a", Node: "n3", Code: 0},
		{Resource: "function b", Node: "m1", Code: 200, Hash: "h2"},
		{Resource: "function c", Node: "k1", Code: 200, Hash: "x"},
		{Resource: "function c", Node: "k2", Code: 200, Hash: "x"},
		{Resource: "function c", Node: "k3", Code: 200, Hash: "y"},
	}})
	s := out.String()
	assert.Assert(t, strings.Contains(lineOf(s, "n1"), "same as other nodes"), s)
	assert.Assert(t, strings.Contains(lineOf(s, "n3"), "no answer"), s)
	assert.Assert(t, !strings.Contains(lineOf(s, "m1"), "other nodes"), s)
	assert.Assert(t, strings.Contains(lineOf(s, "k3"), "DIFFERS"), s)
	assert.Assert(t, !strings.Contains(lineOf(s, "k1"), "DIFFERS"), s)
	for _, l := range strings.Split(s, "\n") {
		assert.Assert(t, l == "" || strings.TrimSpace(l) != "", "padded blank row in %q", s)
	}
}

func TestRepoLabels(t *testing.T) {
	for uri, want := range map[string]string{
		"git@github.com:me/tb_code_shop.git":    "code",
		"git@github.com:me/tb_shop.git":         "config",
		"git@github.com:me/tb_website_site.git": "website site",
		"git@github.com:me/tb_library_lib.git":  "library lib",
		"git@github.com:me/other.git":           "other",
	} {
		assert.Equal(t, repoLabel(patrick.Repository{URI: uri}), want)
	}
}
