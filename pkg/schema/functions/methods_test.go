package functions

import (
	"testing"

	"github.com/taubyte/go-seer"
	"github.com/taubyte/tau/pkg/schema/basic"
	"gotest.tools/v3/assert"
)

func TestMethodsRoot(t *testing.T) {
	d := &function{
		Resource: &basic.Resource{
			Root: func() *seer.Query { return nil },
		},
	}

	var nilQuery *seer.Query
	assert.Equal(t, d.Root(), nilQuery)
}
