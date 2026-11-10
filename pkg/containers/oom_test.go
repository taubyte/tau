package containers

import (
	"context"
	"errors"
	"testing"

	"gotest.tools/v3/assert"
)

func TestOutOfMemoryOnlyWhenConfirmed(t *testing.T) {
	for _, tc := range []struct {
		code      int
		oomKilled bool
		oom       bool
		killed    bool
	}{
		{137, true, true, false},
		{1, true, true, false}, // a child was killed for memory and the build exited on its own
		{137, false, false, true},
		{1, false, false, false},
	} {
		backend := &fakeBackend{exitCode: tc.code, oomKilled: tc.oomKilled}
		_, err := newContainer(backend).Run(context.Background())
		assert.ErrorIs(t, err, ErrorExitCode)
		assert.Equal(t, errors.Is(err, ErrorOutOfMemory), tc.oom, "%d %v: %v", tc.code, tc.oomKilled, err)
		assert.Equal(t, errors.Is(err, ErrorKilled), tc.killed, "%d %v: %v", tc.code, tc.oomKilled, err)
	}
}
