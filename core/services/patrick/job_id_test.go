package patrick

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestPushEventJobID_deterministic(t *testing.T) {
	meta := &Meta{
		Ref:    "refs/heads/main",
		After:  "84cac8e2c33df0ee4400aee496379745be65e8e8",
		Before: "5ab4f01f95993cee73c598e19adae4fd32d94646",
		Repository: Repository{
			ID:       1178849787,
			PushedAt: 1774368594,
		},
	}
	a := PushEventJobID(meta)
	b := PushEventJobID(meta)
	assert.Equal(t, a, b)
	assert.Assert(t, len(a) > 0)
}

func TestPushEventJobID_differentPushedAt_differs(t *testing.T) {
	base := &Meta{
		Ref:   "refs/heads/main",
		After: "84cac8e2c33df0ee4400aee496379745be65e8e8",
		Repository: Repository{
			ID:       1178849787,
			PushedAt: 100,
		},
	}
	other := *base
	other.Repository.PushedAt = 200
	a := PushEventJobID(base)
	b := PushEventJobID(&other)
	assert.Assert(t, a != b)
}
