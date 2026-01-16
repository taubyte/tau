package compile

import (
	"os"
	"reflect"
	"testing"

	"github.com/taubyte/tau/pkg/config-compiler/fixtures"
	"github.com/taubyte/tau/pkg/schema/application"
	"github.com/taubyte/tau/pkg/schema/libraries"
	"github.com/taubyte/tau/pkg/schema/smartops"
)

func TestFunctionSmartOps(t *testing.T) {
	p, err := fixtures.Project()
	if err != nil {
		t.Error(err)
		return
	}
	defer os.RemoveAll("./testGit")

	// Add a new smartop then add the tag to the library to test that a function inherits functions from it's smartops
	smartOp, err := p.SmartOps("test_smartops_g2", "")
	if err != nil {
		t.Error(err)
		return
	}

	err = smartOp.Set(true,
		smartops.Id("testsmartOpsid2"),
		smartops.Timeout("30s"),
		smartops.Memory("64MB"),
		smartops.Call("entryp"),
	)
	if err != nil {
		t.Error(err)
		return
	}

	library, err := p.Library("test_library_l", "")
	if err != nil {
		t.Error(err)
		return
	}

	tags := library.Get().Tags()
	err = library.Set(true, libraries.Tags(append(tags, "smartops:test_smartops_g2")))
	if err != nil {
		t.Error(err)
		return
	}

	// local
	_, returnMap, err := function("test_function_l", "someApp", p)
	if err != nil {
		t.Error(err)
		return
	}
	expected := []string{"QmQ5vhrL7uv6tuoN9KeVBwd4PwfQkXdVVmDLUZuTNxqgvn", "testsmartOpsid2"}
	if reflect.DeepEqual(returnMap["smartops"].([]string), expected) == false {
		t.Errorf("Expected smartops: %v, got: %v", expected, returnMap["smartops"])
		return
	}

	// global
	_, returnMap, err = function("test_function_ghttp", "", p)
	if err != nil {
		t.Error(err)
		return
	}
	if returnMap["smartops"].([]string)[0] != "QmQ5vhrL7uv6tuoN9KeVBwd4PwfQkXdVVmDLUZuTNxqgvm" {
		t.Errorf("Expected smartops: %v, got: %v", "QmQ5vhrL7uv6tuoN9KeVBwd4PwfQkXdVVmDLUZuTNxqgvm", returnMap["smartops"])
		return
	}
}

func TestApplicationSmartOps(t *testing.T) {
	p, err := fixtures.Project()
	if err != nil {
		t.Error(err)
		return
	}
	defer os.RemoveAll("./testGit")

	// Add a new smartop then add the tag to the library to test that a function inherits functions from it's smartops
	smartOp, err := p.SmartOps("test_smartops_g2", "")
	if err != nil {
		t.Error(err)
		return
	}

	err = smartOp.Set(true,
		smartops.Id("testsmartOpsid2"),
		smartops.Timeout("30s"),
		smartops.Memory("64MB"),
		smartops.Call("entryp"),
	)
	if err != nil {
		t.Error(err)
		return
	}

	app, err := p.Application("someApp")
	if err != nil {
		t.Error(err)
		return
	}

	tags := app.Get().Tags()
	err = app.Set(true, application.Tags(append(tags, "smartops:test_smartops_g2")))
	if err != nil {
		t.Error(err)
		return
	}

	// local
	_, returnMap, err := function("test_function_l", "someApp", p)
	if err != nil {
		t.Error(err)
		return
	}
	expected := []string{"QmQ5vhrL7uv6tuoN9KeVBwd4PwfQkXdVVmDLUZuTNxqgvn", "testsmartOpsid2"}
	if reflect.DeepEqual(returnMap["smartops"].([]string), expected) == false {
		t.Errorf("Expected smartops: %v, got: %v", expected, returnMap["smartops"])
		return
	}

}
