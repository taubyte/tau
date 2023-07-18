package structure_test

import (
	"testing"

	"github.com/taubyte/go-interfaces/services/tns"
	structureSpec "github.com/taubyte/go-specs/structure"
)

var (
	testBranch    = "master"
	testProjectId = "testid"
	testAppId     = "someappID"
)

type testStructure[T structureSpec.Structure] struct {
	t                *testing.T
	expectedGlobal   int
	expectedRelative int
	iface            tns.StructureIface[T]
}
