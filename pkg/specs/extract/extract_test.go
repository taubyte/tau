package extract_test

import (
	"fmt"
	"testing"

	"github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/pkg/specs/extract"
	messaging "github.com/taubyte/tau/pkg/specs/messaging"
)

var (
	projectId  = "123456"
	appId      = "someApp1234"
	resourceId = "someMsg123456"
)

type testHelper struct {
	expected string
}

func testExpected(expected string) testHelper {
	return testHelper{expected: expected}
}

func (th testHelper) run(got string) error {

	if got != th.expected {
		return fmt.Errorf("Got `%s` expected `%s`", got, th.expected)
	}

	return nil
}

func TestExtract(t *testing.T) {
	testKey := common.BranchPathVariable.String() + "/" + "master" + "/" + common.ProjectPathVariable.String() + "/" + projectId + "/" + common.ApplicationPathVariable.String() + "/" + appId + "/" + messaging.PathVariable.String() + "/" + resourceId
	path, err := extract.Tns().BasicPath(testKey)
	if err != nil {
		t.Error(err)
		return
	}

	err = testExpected(projectId).run(path.Project())
	if err != nil {
		t.Error(err)
		return
	}

	err = testExpected(appId).run(path.Application())
	if err != nil {
		t.Error(err)
		return
	}

	err = testExpected(resourceId).run(path.Resource())
	if err != nil {
		t.Error(err)
		return
	}
}

func TestExtractApp(t *testing.T) {
	testKey := common.BranchPathVariable.String() + "/" + "master" + "/" + common.ProjectPathVariable.String() + "/" + projectId + "/" + messaging.PathVariable.String() + "/" + resourceId

	path, err := extract.Tns().BasicPath(testKey)
	if err != nil {
		t.Error(err)
		return
	}

	err = testExpected(projectId).run(path.Project())
	if err != nil {
		t.Error(err)
		return
	}

	err = testExpected("inert").run(path.Application())
	if err == nil {
		t.Error("Expected application path to fail")
		return
	}

	err = testExpected(resourceId).run(path.Resource())
	if err != nil {
		t.Error(err)
		return
	}
}

func TestExtractResourceType(t *testing.T) {
	testKey := common.BranchPathVariable.String() + "/" + "master" + "/" + common.ProjectPathVariable.String() + "/" + projectId + "/" + messaging.PathVariable.String() + "/" + resourceId
	testKey2 := common.BranchPathVariable.String() + "/" + "master" + "/" + common.ProjectPathVariable.String() + "/" + projectId + "/" + common.ApplicationPathVariable.String() + "/" + appId + "/" + messaging.PathVariable.String() + "/" + resourceId

	path, err := extract.Tns().BasicPath(testKey)
	if err != nil {
		t.Error(err)
		return
	}

	err = testExpected(messaging.PathVariable.String()).run(path.ResourceType())
	if err != nil {
		t.Error(err)
		return
	}

	path2, err := extract.Tns().BasicPath(testKey2)
	if err != nil {
		t.Error(err)
		return
	}

	err = testExpected(messaging.PathVariable.String()).run(path2.ResourceType())
	if err != nil {
		t.Error(err)
		return
	}
}
