package compile

import (
	"fmt"

	projectSchema "github.com/taubyte/tau/pkg/schema/project"
)

func library(name string, application string, project projectSchema.Project) (_id string, returnMap map[string]interface{}, err error) {
	iFace, err := project.Library(name, application)
	if err != nil {
		return "", nil, err
	}

	getter := iFace.Get()
	_id = getter.Id()

	provider, repo_id, repoFullName := getter.Git()
	returnMap = map[string]interface{}{
		"name":            getter.Name(),
		"description":     getter.Description(),
		"path":            getter.Path(),
		"branch":          getter.Branch(),
		"provider":        provider,
		"repository-id":   repo_id,
		"repository-name": repoFullName,
	}

	_tags := getter.Tags()
	if len(_tags) > 0 {
		returnMap["tags"] = _tags
	}

	err = attachSmartOpsFromTags(returnMap, _tags, application, project, "")
	if err != nil {
		return "", nil, fmt.Errorf("Library( %s/`%s` ): Getting smartOps failed with: %v", application, name, err)
	}

	return _id, returnMap, nil
}
