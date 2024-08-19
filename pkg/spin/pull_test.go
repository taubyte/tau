package spin

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func TestConvImage(t *testing.T) {
	s, err := New(context.TODO(), Runtime[RISCV64]())
	assert.NilError(t, err)
	defer s.Close()

	imageName := "riscv64/hello-world:latest"

	tmp := t.TempDir()
	tmpFile := t.TempDir() + "/riscv64-hello-world.zip"

	err = s.Pull(context.TODO(), imageName, tmp, tmpFile)
	assert.NilError(t, err)

	var buf bytes.Buffer
	c, err := s.New(Bundle(tmpFile), Stdout(&buf), Stderr(&buf))
	assert.NilError(t, err)

	assert.NilError(t, c.Run())

	assert.Equal(t, strings.Contains(buf.String(), "Hello from Docker!"), true)
}
