package smartops_test

import (
	"testing"
	"time"

	"github.com/alecthomas/units"
	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"gotest.tools/v3/assert"
)

func TestStruct(t *testing.T) {
	project, err := internal.NewProjectEmpty()
	assert.NilError(t, err)

	smart, err := project.SmartOps("test_smartops1", "")
	assert.NilError(t, err)

	err = smart.SetWithStruct(true, &structureSpec.SmartOp{
		Id:          "smartops1ID",
		Description: "verifies node has GPU",
		Tags:        []string{"smart_tag_1", "smart_tag_2"},
		Timeout:     uint64(400 * time.Second),
		Memory:      uint64(16 * units.MB),
		Call:        "ping1",
		Source:      ".",
	})
	assert.NilError(t, err)

	assertSmartops1(t, smart.Get())

	smart, err = project.SmartOps("test_smartops1", "")
	assert.NilError(t, err)

	assertSmartops1(t, smart.Get())
}

func TestStructError(t *testing.T) {
	project, err := internal.NewProjectEmpty()
	assert.NilError(t, err)

	smart, err := project.SmartOps("test_smartops1", "")
	assert.NilError(t, err)

	err = smart.SetWithStruct(true, nil)
	assert.ErrorContains(t, err, "nil pointer")
}
