package follow

import (
	"testing"

	"github.com/taubyte/tau/core/services/patrick"
	"gotest.tools/v3/assert"
)

func TestLastJob(t *testing.T) {
	old := job("old", "c1", 1, patrick.JobStatusSuccess)
	old.Timestamp -= 60
	newer := job("new", "c2", 1, patrick.JobStatusFailed)
	site := job("site", "c3", 7, patrick.JobStatusSuccess)
	_, p := newPatrick(t, old, newer, site)

	j, err := LastJob(p, "p", map[int]string{7: "website site1"}, "code")
	assert.NilError(t, err)
	assert.Equal(t, j.Id, "new")

	j, err = LastJob(p, "p", map[int]string{7: "website site1"}, "website site1")
	assert.NilError(t, err)
	assert.Equal(t, j.Id, "site")

	_, err = LastJob(p, "p", nil, "library lib1")
	assert.ErrorContains(t, err, "no build of library lib1")
}
