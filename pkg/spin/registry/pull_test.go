package registry

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/taubyte/tau/pkg/spin/runtime"
	"gotest.tools/v3/assert"
)

func TestPullImage(t *testing.T) {
	root := t.TempDir()
	r, err := New(context.TODO(), root)
	assert.NilError(t, err)
	defer r.Close()

	imageName := "riscv64/hello-world:latest"

	err = r.Pull(context.TODO(), imageName, nil)
	assert.NilError(t, err)

	s, err := runtime.New(context.TODO(), runtime.Runtime[runtime.RISCV64](r))
	assert.NilError(t, err)
	defer s.Close()

	imagePath, err := r.Path(imageName)
	assert.NilError(t, err)

	t.Run("without resolution", func(t *testing.T) {
		var buf bytes.Buffer
		c, err := s.New(runtime.ImageFile(imagePath), runtime.Stdout(&buf), runtime.Stderr(&buf))
		assert.NilError(t, err)

		assert.NilError(t, c.Run())

		assert.Equal(t, strings.Contains(buf.String(), "Hello from Docker!"), true)
	})

	t.Run("with resolution", func(t *testing.T) {
		var buf bytes.Buffer
		c, err := s.New(runtime.Image(imageName), runtime.Stdout(&buf), runtime.Stderr(&buf))
		assert.NilError(t, err)

		assert.NilError(t, c.Run())

		assert.Equal(t, strings.Contains(buf.String(), "Hello from Docker!"), true)
	})

}
