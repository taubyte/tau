package follow

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/taubyte/tau/tools/tau/tcc"
	"gotest.tools/v3/assert"
)

const httpFunctionYAML = `id: QmHelloFunction
description: what tau new function writes
tags: []
source: .
trigger:
    type: https
    method: GET
    paths:
        - /hello
    domains:
        - test_domain1
execution:
    timeout: 5s
    memory: 10MB
    call: hello
`

// Each kind Targets reads, as real project YAML holds it.
func TestDomainsFromRealYAML(t *testing.T) {
	dir := t.TempDir()
	assert.NilError(t, os.CopyFS(dir, os.DirFS("../../../../pkg/tcc/taubyte/v1/fixtures/config")))
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "functions", "hello.yaml"), []byte(httpFunctionYAML), 0644))
	st, err := tcc.OpenAt(dir)
	assert.NilError(t, err)

	for _, c := range []struct{ group, name string }{
		{"functions", "hello"},               // domains under trigger
		{"functions", "test_function1_glob"}, // domains at the top level
		{"websites", "test_website1"},
	} {
		doc, err := st.Doc(c.group, c.name)
		assert.NilError(t, err, c.name)
		assert.DeepEqual(t, domainsOf(doc), []any{"test_domain1"})
	}
	doc, err := st.Doc("functions", "hello")
	assert.NilError(t, err)
	assert.Equal(t, tcc.Get(doc, []string{"trigger", "type"}), "https")
	assert.Equal(t, firstString(tcc.Get(doc, []string{"trigger", "paths"})), "/hello")
	doc, err = st.Doc("websites", "test_website1")
	assert.NilError(t, err)
	assert.Equal(t, firstString(tcc.Get(doc, []string{"source", "paths"})), "/photos")
	d, err := st.Doc("domains", "test_domain1")
	assert.NilError(t, err)
	assert.Equal(t, tcc.Get(d, []string{"fqdn"}), "hal.computers.com")
}
