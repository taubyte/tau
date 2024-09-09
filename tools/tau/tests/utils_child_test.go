package tests

import (
	"fmt"
	"testing"

	"gotest.tools/v3/assert"
)

func (tm *monkeyTestContext) validateBabyMonkey(bm testMonkey) error {
	if len(bm.preRun) > 0 {
		return fmt.Errorf("A baby monkey `%s` does not support attribute: preRun", bm.name)
	}

	if len(bm.children) > 0 {
		return fmt.Errorf("A baby monkey `%s` does not support attribute: children", bm.name)
	}

	if len(bm.env) > 0 {
		return fmt.Errorf("A baby monkey `%s` does not support attribute: env", bm.name)
	}

	if !tm.mock && bm.mock {
		return fmt.Errorf("A baby monkey `%s` does not support attribute: mock, define it on the parent", bm.name)
	}

	if bm.writeFilesInDir != nil {
		return fmt.Errorf("A baby monkey `%s` does not support attribute: writeFilesInDir, define it on the parent", bm.name)
	}

	return nil
}

func (tm *monkeyTestContext) runBabyMonkeys(t *testing.T, rr roadRunner) {
	if tm.children != nil {
		for _, bm := range tm.children {
			t.Run(bm.name, func(t *testing.T) {
				bm.debug = tm.debug

				err := tm.validateBabyMonkey(bm)
				assert.NilError(t, err)

				newMonkeyRunContext(bm, rr, true).Run(t)
			})
		}
	}
}
