package config_test

import (
	"fmt"
	"path"
	"testing"

	"github.com/taubyte/tau/tools/tau/singletons/config"
)

func TestProjects(t *testing.T) {
	cwd, deferment, err := initializeTest()
	if err != nil {
		t.Error(err)
		return
	}
	defer deferment()

	testProjectName := "test_project"
	testProject := config.Project{
		DefaultProfile: "someProfile",
		Location:       path.Join(cwd, "test_project"),
	}

	projects := config.Projects()

	err = projects.Set(testProjectName, testProject)
	if err != nil {
		t.Error(err)
		return
	}

	project, err := projects.Get(testProjectName)
	if err != nil {
		t.Error(err)
		return
	}

	if project != testProject {
		t.Errorf("Expected %v, got %v", testProject, project)
		return
	}

	expectedData := `projects:
    test_project:
        default_profile: someProfile
        location: %s/test_project
`

	configData, err := readConfig()
	if err != nil {
		t.Error(err)
		return
	}

	expectedData = fmt.Sprintf(expectedData, cwd)

	if configData != expectedData {
		t.Errorf("Expected %s, got %s", expectedData, configData)
		return
	}
}
