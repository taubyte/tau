package config

import (
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/tau/constants"
	"gotest.tools/v3/assert"
)

// A TAU_CONFIG_FILE this OS cannot open (a Git Bash /c/... path on Windows) is
// a clear error, not a panic.
func TestLoadConfigBadPathIsClearError(t *testing.T) {
	defer func(old string) { constants.TauConfigFileName = old; Clear() }(constants.TauConfigFileName)
	constants.TauConfigFileName = "/no/such/dir/c/Users/x/tau.yaml"
	err := loadConfig()
	assert.Assert(t, err != nil)
	assert.Assert(t, strings.Contains(err.Error(), "TAU_CONFIG_FILE"), err.Error())
}
