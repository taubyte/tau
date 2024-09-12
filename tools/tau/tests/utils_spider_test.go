package tests

import (
	"os"
	"testing"

	"github.com/taubyte/tau/pkg/cli/common"
)

func newSpider(s *testSpider, parallel bool, debug ...bool) *spiderTestContext {
	debuggingMode := (len(debug) > 0 && debug[0])
	for _, ti := range s.tests {
		if ti.debug {
			debuggingMode = true
		}
	}

	return &spiderTestContext{
		testSpider: s,
		parallel:   parallel,
		debug:      debuggingMode,
	}
}

// Run the method to get the config file []byte
// and write it to the temp directory
func (s *testSpider) writeConfig(dir, configLoc string) error {
	if s.getConfigString != nil {
		return os.WriteFile(configLoc, s.getConfigString(dir), common.DefaultFilePermission)
	}

	return nil
}

func (s *testSpider) getBeforeEach(tm testMonkey) [][]string {
	beforeEach := make([][]string, 0)
	if s.beforeEach != nil {
		args := s.beforeEach(tm)
		if args != nil {
			beforeEach = append(beforeEach, args...)
		}
	}

	return beforeEach
}

func (s *spiderTestContext) Run(t *testing.T) {
	for _, ti := range s.tests {
		// Credits: parallel
		// https://gist.github.com/posener/92a55c4cd441fc5e5e85f27bca008721
		tm := ti
		if s.debug && !tm.debug {
			continue
		}

		monkeyTest := newMonkey(s, tm)
		t.Run(tm.name, monkeyTest.Run)
	}
}
