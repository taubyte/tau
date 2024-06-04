package compile

import (
	"github.com/taubyte/tau/pkg/config-compiler/indexer"
)

func (c *compiler) application(name string) (appID string, appObject map[string]interface{}, err error) {
	app, err := c.config.Project.Application(name)
	if err != nil {
		return
	}
	getter := app.Get()
	appID = getter.Id()
	if len(appID) == 0 {
		return "", nil, nil
	}
	appObject = map[string]interface{}{
		"name":        getter.Name(),
		"description": getter.Description(),
	}

	_tags := getter.Tags()
	if len(_tags) > 0 {
		appObject["tags"] = _tags
	}

	ctx := &indexer.IndexContext{
		AppId:                 appID,
		AppName:               name,
		Branch:                c.ctx.Branch,
		ProjectId:             c.ctx.ProjectId,
		Commit:                c.ctx.Commit,
		Obj:                   appObject,
		Dev:                   c.dev,
		GeneratedDomainRegExp: c.config.GeneratedDomainRegExp,
	}
	for _type, iFace := range compilationGroup(c.config.Project) {
		local, _ := iFace.Get(name)
		if len(local) > 0 {
			appObject[_type], err = c.magic(local, name, iFace.Compile)
			if err != nil {
				return
			}

			if iFace.Indexer != nil {
				err = c.indexer(ctx, iFace.Indexer)
				if err != nil {
					return
				}
			}
		}
	}

	return
}
