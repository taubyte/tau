package tests

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/taubyte/tau/pkg/cli/common"
	"gotest.tools/v3/assert"
)

var (
	random = rand.New(rand.NewSource(42))

	// Used for locking the singletons session for evaluation in tests
	sessionLock sync.Mutex
)

func cleanArgs(args []string) string {
	var newArgs string
	for idx, arg := range args {
		if idx > 0 {
			newArgs += " "
		}
		// If it contains spaces, wrap it with ""
		if strings.Contains(arg, " ") {
			newArgs += fmt.Sprintf(`"%s"`, arg)
		} else {
			newArgs += arg
		}
	}

	return "tau " + newArgs
}

func startMockOnPort(port string) context.CancelFunc {
	ctx, ctxC := context.WithCancel(context.Background())

	cmd := exec.CommandContext(ctx, "python3", "mock", port)

	// Capture command output
	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	err := cmd.Start()
	if err != nil {
		panic(fmt.Sprintf("running mock server failed with: %s\nOUT: %s\nERR: %s\n", err, out.String(), errOut.String()))
	}

	return ctxC
}

func runTests(t *testing.T, s *testSpider, parallel bool, debug ...bool) {
	err := buildTau()
	assert.NilError(t, err)

	// check if "./_fakeroot exists", if not create it
	_, err = os.Stat("./_fakeroot")
	if os.IsNotExist(err) {
		err = os.Mkdir("./_fakeroot", common.DefaultDirPermission)
		assert.NilError(t, err)
	}

	spider := newSpider(s, parallel, debug...)

	// Iterate through the monkey tests
	t.Run(s.testName, spider.Run)
}
