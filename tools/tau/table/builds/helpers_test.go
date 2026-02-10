package buildsTable

import (
	"testing"

	"github.com/taubyte/tau/core/services/patrick"
	"gotest.tools/v3/assert"
)

func minimalJob(id string, ts int64) *patrick.Job {
	return &patrick.Job{
		Id:        id,
		Timestamp: ts,
		Status:    patrick.JobStatusSuccess,
		Meta: patrick.Meta{
			HeadCommit: patrick.HeadCommit{ID: "abc"},
			Repository: patrick.Repository{ID: 1},
		},
	}
}

func TestJobArray_LenSwapLessString(t *testing.T) {
	j1 := minimalJob("1", 100)
	j2 := minimalJob("2", 200)
	a := jobArray{j1, j2}

	assert.Equal(t, a.Len(), 2)
	// Less(i,j) = a[i].Timestamp > a[j].Timestamp (descending)
	assert.Assert(t, !a.Less(0, 1)) // 100 > 200 is false
	assert.Assert(t, a.Less(1, 0))  // 200 > 100 is true

	a.Swap(0, 1)
	assert.Equal(t, a[0].Id, "2")
	assert.Equal(t, a[1].Id, "1")

	s := a.String()
	assert.Assert(t, len(s) > 0)
}
