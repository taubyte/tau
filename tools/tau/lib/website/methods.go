package websiteLib

import (
	"fmt"

	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func New(website *structureSpec.Website) error {
	return set(website, true)
}

func Set(website *structureSpec.Website) error {
	return set(website, false)
}

func Delete(name string) error {
	info, err := get(name)
	if err != nil {
		return err
	}

	return info.website.Delete()
}

func List() ([]string, error) {
	_, _, websites, err := list()
	if err != nil {
		return nil, err
	}

	return websites, nil
}

func ListResources() ([]*structureSpec.Website, error) {
	project, application, relative, err := list()
	if err != nil {
		return nil, err
	}

	websites := make([]*structureSpec.Website, len(relative))
	for idx, name := range relative {
		website, err := project.Website(name, application)
		if err != nil {
			return nil, err
		}

		websites[idx], err = website.Get().Struct()
		if err != nil {
			return nil, err
		}
	}

	return websites, nil
}

func GetRepositoryUrl(website *structureSpec.Website) string {
	switch website.Provider {
	case "github":
		return "https://github.com/" + website.RepoName
	default:
		panic(fmt.Sprintf("url for provider: `%s` not found", website.Provider))
	}
}
