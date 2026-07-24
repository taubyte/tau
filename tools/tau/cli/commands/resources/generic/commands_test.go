package generic_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/tau/cli"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

func run(args ...string) error {
	return cli.Run(append([]string{"tau", "--defaults", "--yes", "--color", "never"}, args...)...)
}

// Every kind the DSL defines answers query/list off the fixture, which carries
// one of each — so the read/table path is exercised for all of them at once,
// no per-kind code.
func TestQueryListEveryKind(t *testing.T) {
	testutil.WithTCCFixtureCopyEnv(t)
	kinds := []string{"function", "database", "domain", "library", "messaging", "service", "smartops", "storage", "website", "application"}
	for _, k := range kinds {
		t.Run(k, func(t *testing.T) {
			assert.NilError(t, run("query", k, "--list"))
			assert.NilError(t, run("list", k))
		})
	}
}

// One create per widget family that the function/storage tests don't already
// cover: a show-when block (domain cert), an enum + scalar (database), booleans
// (messaging), and a plain scalar (service).
func TestCreateAcrossWidgets(t *testing.T) {
	root := testutil.WithTCCFixtureCopyEnv(t)
	config := filepath.Join(root, "config")

	cases := []struct {
		kind  string
		name  string
		args  []string
		file  string
		wants []string
	}{
		{
			kind: "domain", name: "d1",
			args:  []string{"--fqdn", "x.example.com", "--type", "inline", "--cert", "C", "--key", "K"},
			file:  "domains/d1.yaml",
			wants: []string{"fqdn: x.example.com", "type: inline", "cert: C"},
		},
		{
			kind: "database", name: "db1",
			args:  []string{"--match", "/k", "--network", "host", "--size", "1GB"},
			file:  "databases/db1.yaml",
			wants: []string{"match: /k", "network: host", "size: 1GB"},
		},
		{
			kind: "messaging", name: "m1",
			args:  []string{"--match", "topic", "--mqtt", "--websocket"},
			file:  "messaging/m1.yaml",
			wants: []string{"match: topic", "mqtt:", "websocket:"},
		},
		{
			kind: "service", name: "s1",
			args:  []string{"--protocol", "/x/1.0"},
			file:  "services/s1.yaml",
			wants: []string{"protocol: /x/1.0"},
		},
	}
	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			assert.NilError(t, run(append([]string{"new", c.kind, c.name}, c.args...)...))
			yaml := readFile(t, filepath.Join(config, c.file))
			for _, w := range c.wants {
				assert.Assert(t, strings.Contains(yaml, w), "want %q in:\n%s", w, yaml)
			}
			// query the created resource (detail table)
			assert.NilError(t, run("query", c.kind, c.name))
		})
	}
}

// A domain's TLS fields are conditional on the certificate type; choosing "auto"
// must not carry the inline cert/key.
func TestShowWhenConditional(t *testing.T) {
	root := testutil.WithTCCFixtureCopyEnv(t)
	assert.NilError(t, run("new", "domain", "auto_dom", "--fqdn", "a.example.com", "--type", "auto"))
	yaml := readFile(t, filepath.Join(root, "config", "domains", "auto_dom.yaml"))
	assert.Assert(t, strings.Contains(yaml, "type: auto"), yaml)
	assert.Assert(t, !strings.Contains(yaml, "cert:"), "inline cert should be absent:\n%s", yaml)
}

// run is only for HTTP(S) code-backed kinds; a non-http trigger is refused
// before any wasm is touched, and a missing wasm is reported.
func TestRunErrors(t *testing.T) {
	testutil.WithTCCFixtureCopyEnv(t)

	// test_function1_glob is http; point it at a wasm that isn't there
	err := cli.Run("tau", "--defaults", "run", "function", "--name", "test_function1_glob", "--wasm", "/no/such.wasm")
	assert.ErrorContains(t, err, "wasm file")

	// a pubsub function can't be run
	err = cli.Run("tau", "--defaults", "run", "function", "--name", "test_function3_glob")
	assert.Assert(t, err != nil)
}

// A resource that doesn't exist is a clear error, not a panic.
func TestQueryMissing(t *testing.T) {
	testutil.WithTCCFixtureCopyEnv(t)
	err := cli.Run("tau", "--defaults", "--color", "never", "query", "function", "--name", "ghost")
	assert.ErrorContains(t, err, "not found")
}

// The application scope can be entered and cleared by command as well as
// implicitly on create.
func TestSelectScopeCommands(t *testing.T) {
	testutil.WithTCCFixtureCopyEnv(t)
	assert.NilError(t, run("select", "application", "test_app1"))
	assert.NilError(t, run("select", "application", "--none"))
	// --name and --none together is refused
	err := run("select", "application", "test_app1", "--none")
	assert.ErrorContains(t, err, "cannot use")
	assert.NilError(t, run("clear", "application"))
}
