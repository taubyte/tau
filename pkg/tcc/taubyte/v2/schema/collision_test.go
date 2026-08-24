package schema_test

import (
	"context"
	"testing"

	"github.com/spf13/afero"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

// A COLLISION NAMES THE TWO THINGS CAUSING IT. Both sides of a name a workflow produces
// twice are the same workflow, so reporting the source's id said "by X and by X" — the
// author is told which name collided and never which step.
func TestProducedTwiceNamesTheStep(t *testing.T) {
	fs := afero.NewMemMapFs()
	for path, body := range map[string]string{
		"/project.yaml":    "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nname: p\n",
		"/function/a.yaml": "id: QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh\nsource: \".\"\nexecution:\n    call: a\n    timeout: 10s\n    memory: 16MB\n",
		"/function/b.yaml": "id: QmTaCZBnrxvJm5xkLQKN4o3TFhBqHfLYCUuCkFTGiT8s8t\nsource: \".\"\nexecution:\n    call: b\n    timeout: 10s\n    memory: 16MB\n",
		// two steps called the same thing, so both derive queue/w_s
		"/workflow/w.yaml": "id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zb\nstart: s\nsteps:\n    - name: s\n      call:\n          function: a\n    - name: s\n      call:\n          function: b\n",
	} {
		assert.NilError(t, afero.WriteFile(fs, path, []byte(body), 0644))
	}

	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	_, _, err = c.Compile(context.Background())
	assert.ErrorContains(t, err, "function/w_s is produced twice")
	assert.ErrorContains(t, err, "steps/0", "the error must say which step: %v", err)
	assert.ErrorContains(t, err, "steps/1", "the error must say which step: %v", err)
	assert.ErrorContains(t, err, "workflow/w", "and which resource to go and edit: %v", err)
}
