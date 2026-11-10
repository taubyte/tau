package cli

import (
	"testing"

	"gotest.tools/v3/assert"
)

// push project follows its builds, and status shows the project in one screen.
func TestPushWaitAndStatusCommands(t *testing.T) {
	app, err := New()
	assert.NilError(t, err)

	names := map[string]map[string]bool{}
	for _, c := range app.Commands {
		names[c.Name] = map[string]bool{}
		if c.Name == "push" {
			for _, sub := range c.Subcommands {
				if sub.Name == "project" {
					for _, f := range sub.Flags {
						for _, n := range f.Names() {
							names["push"][n] = true
						}
					}
				}
			}
		}
		if c.Name == "status" {
			for _, f := range c.Flags {
				for _, n := range f.Names() {
					names["status"][n] = true
				}
			}
		}
	}
	for _, f := range []string{"wait", "serve", "timeout"} {
		assert.Assert(t, names["push"][f], "push project --%s", f)
	}
	assert.Assert(t, names["status"] != nil, "tau status")
	assert.Assert(t, names["status"]["watch"], "tau status --watch")
}
