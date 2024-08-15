package spin

import (
	"context"
	"testing"

	"gotest.tools/v3/assert"
)

func TestNew(t *testing.T) {
	_, err := New(context.Background())
	assert.NilError(t, err)
}

func TestNewContainer(t *testing.T) {
	for name, opts := range map[string][]Option[Spin]{
		"using amd64":   {Runtime[AMD64]()},
		"using riscv64": {Runtime[RISCV64]()},
	} {
		t.Run(name, func(t *testing.T) {
			s, err := New(context.Background(), opts...)
			assert.NilError(t, err)

			c, err := s.New()
			assert.NilError(t, err)

			c2, err := s.New()
			assert.NilError(t, err)

			c3, err := s.New()
			assert.NilError(t, err)

			go c.Run()
			go c2.Run()
			c3.Run()
		})
	}
}
