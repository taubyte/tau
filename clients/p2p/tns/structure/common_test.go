package structure_test

import (
	"testing"

	"github.com/taubyte/tau/core/services/tns"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

var (
	testBranches  = []string{"master", "main"}
	testProjectId = "QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR"
	testAppId     = "QmZvW43kx7p8v5dZ1qV8WFtxtBnJA6Cr6pcZXp6p4L9kC3"
)

type testStructure[T structureSpec.Structure] struct {
	t                *testing.T
	expectedGlobal   int
	expectedRelative int
	iface            tns.StructureIface[T]
}
