package smartops_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/smartops"
	"gotest.tools/v3/assert"
)

func TestDeleteBasic(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	smart, err := project.SmartOps("test_smartops2", "test_app1")
	assert.NilError(t, err)

	assertSmartops2(t, smart.Get())

	err = smart.Delete()
	assert.NilError(t, err)
	internal.AssertEmpty(t,
		smart.Get().Id(),
		smart.Get().Name(),
		smart.Get().Description(),
		smart.Get().Tags(),
		smart.Get().Source(),
		smart.Get().Timeout(),
		smart.Get().Memory(),
		smart.Get().Call(),
	)

	local, _ := project.Get().SmartOps("test_app1")
	assert.Equal(t, len(local), 0)

	smart, err = project.SmartOps("test_smartops2", "test_app1")
	assert.NilError(t, err)

	assert.Equal(t, smart.Get().Name(), "test_smartops2")
	internal.AssertEmpty(t,
		smart.Get().Id(),
		smart.Get().Description(),
		smart.Get().Tags(),
		smart.Get().Source(),
		smart.Get().Timeout(),
		smart.Get().Memory(),
		smart.Get().Call(),
	)
}

func TestDeleteAttributes(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	smart, err := project.SmartOps("test_smartops1", "")
	assert.NilError(t, err)

	assertSmartops1(t, smart.Get())

	err = smart.Delete("description", "source")
	assert.NilError(t, err)

	assertion := func(_smart smartops.SmartOps) {
		eql(t, [][]any{
			{_smart.Get().Id(), "smartops1ID"},
			{_smart.Get().Name(), "test_smartops1"},
			{_smart.Get().Description(), ""},
			{_smart.Get().Tags(), []string{"smart_tag_1", "smart_tag_2"}},
			{_smart.Get().Source(), ""},
			{_smart.Get().Timeout(), "6m40s"},
			{_smart.Get().Memory(), "16MB"},
			{_smart.Get().Call(), "ping1"},
			{_smart.Get().Application(), ""},
		})
	}
	assertion(smart)

	// Re-open
	smart, err = project.SmartOps("test_smartops1", "")
	assert.NilError(t, err)

	assert.Equal(t, smart.Get().Id(), "smartops1ID")
	assert.Equal(t, smart.Get().Name(), "test_smartops1")
	assertion(smart)
}
