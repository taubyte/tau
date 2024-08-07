package structure_test

import (
	"testing"

	"github.com/taubyte/tau/core/services/tns"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

var (
	testBranches  = []string{"master", "main"}
	testProjectId = "testid"
	testAppId     = "someappID"
)

type testStructure[T structureSpec.Structure] struct {
	t                *testing.T
	expectedGlobal   int
	expectedRelative int
	iface            tns.StructureIface[T]
}
