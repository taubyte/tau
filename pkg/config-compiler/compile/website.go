package compile

import (
	"fmt"

	projectSchema "github.com/taubyte/tau/pkg/schema/project"
)

func website(name string, application string, project projectSchema.Project) (_id string, returnMap map[string]interface{}, err error) {
	iFace, err := project.Website(name, application)
	if err != nil {
		return "", nil, err
	}

	getter := iFace.Get()
	_id = getter.Id()

	provider, repo_id, repoFullName := getter.Git()
	returnMap = map[string]interface{}{
		"name":            getter.Name(),
		"description":     getter.Description(),
		"paths":           getter.Paths(),
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
		return "", nil, fmt.Errorf("Website( %s/`%s` ): Getting smartOps failed with: %v", application, name, err)
	}

	_domains := getter.Domains()
	if len(_domains) > 0 {
		domIDs, err := getDomIDs(_domains, application, project)
		if err != nil {
			return "", nil, fmt.Errorf("Website( %s/`%s` ): Getting domains failed with: %v", application, name, err)
		}
		if len(domIDs) > 0 {
			returnMap["domains"] = domIDs
		}
	}
	return _id, returnMap, nil
}
