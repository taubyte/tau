package monkey

import (
	"errors"
	"fmt"
	"testing"

	"github.com/taubyte/tau/pkg/builder"
	"github.com/taubyte/tau/pkg/containers"
)

func TestFailReasonOnlyForOutOfMemory(t *testing.T) {
	oom := builder.OutOfMemoryError{Limit: 4 << 30}
	if got := failReason(fmt.Errorf("building failed with: %w: exit-code:137", oom)); got != oom.Error() {
		t.Fatalf("an out of memory failure must not be retried and must say why, got %q", got)
	}
	// exit 137 not confirmed as out of memory is any SIGKILL: it keeps its retry
	if got := failReason(fmt.Errorf("building failed with: %w:137 (%w)", containers.ErrorExitCode, containers.ErrorKilled)); got != "" {
		t.Fatalf("a kill not confirmed as out of memory must be retried, got reason %q", got)
	}
	if got := failReason(errors.New("compile error")); got != "" {
		t.Fatalf("an ordinary failure may be retried, got reason %q", got)
	}
}
