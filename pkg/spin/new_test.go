package spin

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func TestNew(t *testing.T) {
	_, err := New(context.Background())
	assert.NilError(t, err)
}

func TestNewContainer(t *testing.T) {
	for arch, opts := range map[string][]Option[Spin]{
		"amd64":   {Runtime[AMD64]()},
		"riscv64": {Runtime[RISCV64]()},
	} {
		t.Run("using "+arch, func(t *testing.T) {
			s, err := New(context.Background(), opts...)
			assert.NilError(t, err)

			var buf bytes.Buffer

			c, err := s.New(
				Bundle(fmt.Sprintf("fixtures/%s-hello-world.zip", arch)),
				Stdout(&buf), Stderr(&buf),
			)
			assert.NilError(t, err)

			err = c.Run()
			assert.NilError(t, err)

			assert.Equal(t, strings.Contains(buf.String(), "Hello from Docker!"), true)
		})
	}
}

func TestPreBuiltContainer(t *testing.T) {
	squashSrc, err := toolsSquashFS()
	assert.NilError(t, err)

	s, err := New(context.Background(), Module(squashSrc))
	assert.NilError(t, err)

	var buf bytes.Buffer
	c, err := s.New(Command("uname", "-a"), Stdout(&buf), Stderr(&buf))
	assert.NilError(t, err)

	err = c.Run()
	assert.NilError(t, err)

	assert.Equal(t, strings.Contains(buf.String(), "riscv64 Linux"), true)
}
