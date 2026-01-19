package indexer

import (
	"fmt"

	"github.com/taubyte/tau/core/common"
	projectSchema "github.com/taubyte/tau/pkg/schema/project"

	"github.com/taubyte/tau/pkg/specs/methods"
	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
	"github.com/taubyte/tau/utils/maps"
)

func Websites(ctx *IndexContext, project projectSchema.Project, urlIndex map[string]interface{}) error {
	if urlIndex == nil {
		return fmt.Errorf("urlIndex received is nil")
	}

	if ctx.Obj == nil {
		return fmt.Errorf("obj received is nil")
	}

	if ctx.Commit == "" || ctx.Branch == "" || ctx.ProjectId == "" {
		return fmt.Errorf("commit, branch, and project required for IndexContext: `%v`", ctx)
	}

	websitesObj, ok := ctx.Obj[string(websiteSpec.PathVariable)]
	if !ok {
		return nil // This shouldn't be breaking,  it just means there are no websites
	}
	for _, website := range maps.SafeInterfaceToStringKeys(websitesObj) {
		name, err := maps.String(maps.SafeInterfaceToStringKeys(website), "name")
		if err != nil {
			return err
		}

		web, err := project.Website(name, ctx.AppName)
		if err != nil {
			return err
		}

		getter := web.Get()
		webId := getter.Id()
		if len(webId) == 0 {
			return fmt.Errorf("website `%s` not found", getter.Name())
		}

		// set repository path
		gitProvider, repoId, _ := getter.Git()
		_path, err := methods.GetRepositoryPath(gitProvider, repoId, ctx.ProjectId)
		if err != nil {
			return err
		}

		urlIndex[_path.Type().String()] = common.WebsiteRepository
		tnsPath, err := websiteSpec.Tns().IndexValue(ctx.Branch, ctx.ProjectId, ctx.AppId, webId)
		if err != nil {
			return err
		}

		value := tnsPath.String()
		urlIndex[_path.Resource(webId).String()] = value

		// set domain paths
		for _, domain := range getter.Domains() {
			domObj, err := getDomain(domain, ctx.AppName, project)
			if err != nil {
				return err
			}

			httpPath, err := websiteSpec.Tns().HttpPath(domObj.Get().FQDN())
			if err != nil {
				return err
			}

			// create entry if empty
			linksPath := httpPath.Versioning().Links().String()
			if _, exists := urlIndex[linksPath]; !exists {
				//TODO: Add capacity to all makes, make variable in specs for default capacity
				urlIndex[linksPath] = make([]string, 0)
			}

			// check if value not there already
			skip := false
			for _, val := range urlIndex[linksPath].([]string) {
				if value == val {
					skip = true
					break
				}
			}

			// add value (path to object) to the list
			if !skip {
				urlIndex[linksPath] = append(urlIndex[linksPath].([]string), value)
			}
		}
	}

	return nil
}
