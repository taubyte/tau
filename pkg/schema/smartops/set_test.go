package smartops_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/smartops"
	"gotest.tools/v3/assert"
)

func TestSetBasic(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	smrt, err := project.SmartOps("test_smartops1", "")
	assert.NilError(t, err)

	assertSmartops1(t, smrt.Get())

	var (
		id          = "service3ID"
		description = "this is test smrt 3"
		tags        = []string{"smrt_tag_5", "smrt_tag_6"}
		source      = "library/test-library"
		timeout     = "1h10m"
		memory      = "64GB"
		call        = "ping42"
	)

	err = smrt.Set(true,
		smartops.Id(id),
		smartops.Description(description),
		smartops.Tags(tags),
		smartops.Source(source),
		smartops.Timeout(timeout),
		smartops.Memory(memory),
		smartops.Call(call),
	)
	assert.NilError(t, err)

	assertion := func(_smrt smartops.SmartOps) {
		eql(t, [][]any{
			{_smrt.Get().Id(), id},
			{_smrt.Get().Name(), "test_smartops1"},
			{_smrt.Get().Description(), description},
			{_smrt.Get().Tags(), tags},
			{_smrt.Get().Source(), source},
			{_smrt.Get().Timeout(), timeout},
			{_smrt.Get().Memory(), memory},
			{_smrt.Get().Call(), call},
			{_smrt.Get().Application(), ""},
		})
	}
	assertion(smrt)

	smrt, err = project.SmartOps("test_smartops1", "")
	assert.NilError(t, err)

	assertion(smrt)
}

func TestSetInApp(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	smrt, err := project.SmartOps("test_smartops2", "test_app1")
	assert.NilError(t, err)

	assertSmartops2(t, smrt.Get())

	var (
		id          = "service3ID"
		description = "this is test smrt 3"
		tags        = []string{"smrt_tag_5", "smrt_tag_6"}
		source      = "library/test-library"
		timeout     = "1h10m"
		memory      = "64GB"
		call        = "ping42"
	)

	err = smrt.Set(true,
		smartops.Id(id),
		smartops.Description(description),
		smartops.Tags(tags),
		smartops.Source(source),
		smartops.Timeout(timeout),
		smartops.Memory(memory),
		smartops.Call(call),
	)
	assert.NilError(t, err)

	assertion := func(_smrt smartops.SmartOps) {
		eql(t, [][]any{
			{_smrt.Get().Id(), id},
			{_smrt.Get().Name(), "test_smartops2"},
			{_smrt.Get().Description(), description},
			{_smrt.Get().Tags(), tags},
			{_smrt.Get().Source(), source},
			{_smrt.Get().Timeout(), timeout},
			{_smrt.Get().Memory(), memory},
			{_smrt.Get().Call(), call},
			{_smrt.Get().Application(), "test_app1"},
		})
	}
	assertion(smrt)

	smrt, err = project.SmartOps("test_smartops2", "test_app1")
	assert.NilError(t, err)

	assertion(smrt)
}
