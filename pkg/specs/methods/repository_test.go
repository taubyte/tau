package methods

import "testing"

var (
	gitProvider = "github"
	repoId      = "3812394"
	projectId   = "some123project"
	resource    = "someResource123"
)

func TestRepository(t *testing.T) {
	repoPath, err := GetRepositoryPath(gitProvider, repoId, projectId)
	if err != nil {
		t.Error(err)
		return
	}

	expectedString := RepositoryPathVariable.String() + "/" + gitProvider + "/" + repoId + "/" + projectId + "/" + TypePathVariable.String()
	if repoPath.Type().String() != expectedString {
		t.Errorf("Got `%s` expected `%s`", repoPath.Type(), expectedString)
		return
	}

	expectedString = RepositoryPathVariable.String() + "/" + gitProvider + "/" + repoId + "/" + projectId + "/" + ResourcePathVariable.String() + "/" + resource
	if repoPath.Resource(resource).String() != expectedString {
		t.Errorf("Got `%s` expected `%s`", repoPath.Resource(resource), expectedString)
		return
	}

	expectedString = RepositoryPathVariable.String() + "/" + gitProvider + "/" + repoId + "/" + projectId + "/" + ResourcePathVariable.String()
	if repoPath.AllResources().String() != expectedString {
		t.Errorf("Got `%s` expected `%s`", repoPath.AllResources(), expectedString)
		return
	}
}
