package constants_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/constants"
	"gotest.tools/v3/assert"
)

func TestCodeTypes(t *testing.T) {
	assert.Equal(t, len(constants.CodeTypes), 2)
	assert.Equal(t, len(constants.CodeExts), 2)
	assert.Equal(t, constants.CodeNames["Go"], ".go")
	assert.Equal(t, constants.CodeNames["AssemblyScript"], ".ts")
	assert.Equal(t, constants.ReverseName[".go"], "Go")
	assert.Equal(t, constants.ReverseName[".ts"], "AssemblyScript")
}

func TestTauConfigFileNameSet(t *testing.T) {
	assert.Assert(t, len(constants.TauConfigFileName) > 0)
}
