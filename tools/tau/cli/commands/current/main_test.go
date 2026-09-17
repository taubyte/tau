package current

import (
	"os"
	"testing"

	"github.com/taubyte/tau/tools/tau/testutil"
)

// Every test here builds some part of the CLI, and building it resolves the
// selected project — so without this they load, and write, the developer's own
// config. See testutil.RunWithIsolatedConfig.
func TestMain(m *testing.M) { os.Exit(testutil.RunWithIsolatedConfig(m)) }
