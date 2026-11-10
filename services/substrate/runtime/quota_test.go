package runtime

import (
	"testing"

	"github.com/taubyte/tau/pkg/config"
	"github.com/taubyte/tau/pkg/platformerr"
)

func TestProjectInstanceCap(t *testing.T) {
	caps := config.Caps{Instances: 2, FunctionMemory: 100}
	if acquire("p", 10, caps) != nil || acquire("p", 10, caps) != nil {
		t.Fatal("instances under the cap must be given")
	}
	if err := acquire("p", 10, caps); platformerr.CodeOf(err) != platformerr.OverQuota {
		t.Fatalf("third instance: %v", err)
	}
	if acquire("other", 10, caps) != nil {
		t.Fatal("another project is not affected")
	}
	release("p", 10)
	if acquire("p", 10, caps) != nil {
		t.Fatal("a released instance frees its slot")
	}
	if err := acquire("p", 101, caps); platformerr.CodeOf(err) != platformerr.OverQuota {
		t.Fatalf("function memory over the cap: %v", err)
	}
	if acquire("free", 1<<40, config.Caps{}) != nil {
		t.Fatal("zero caps are unlimited")
	}
}
