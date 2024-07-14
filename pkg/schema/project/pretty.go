package project

import (
	"fmt"

	"github.com/taubyte/tau/pkg/schema/pretty"
)

func (p *project) ResourceMethods() []pretty.PrettyResourceIface {
	getter := p.Get()
	return []pretty.PrettyResourceIface{
		{
			Type: "Services",
			Get: func(name, application string) (pretty.PrettyResource, error) {
				return p.Service(name, application)
			},
			List: getter.Services,
		},
		{
			Type: "Libraries",
			Get: func(name, application string) (pretty.PrettyResource, error) {
				return p.Library(name, application)
			},
			List: getter.Libraries,
		},
		{
			Type: "Websites",
			Get: func(name, application string) (pretty.PrettyResource, error) {
				return p.Website(name, application)
			},
			List: getter.Websites,
		},
		{
			Type: "Messaging",
			Get: func(name, application string) (pretty.PrettyResource, error) {
				return p.Messaging(name, application)
			},
			List: getter.Messaging,
		},
		{
			Type: "Databases",
			Get: func(name, application string) (pretty.PrettyResource, error) {
				return p.Database(name, application)
			},
			List: getter.Databases,
		},
		{
			Type: "Storages",
			Get: func(name, application string) (pretty.PrettyResource, error) {
				return p.Storage(name, application)
			},
			List: getter.Storages,
		},
		{
			Type: "Domains",
			Get: func(name, application string) (pretty.PrettyResource, error) {
				return p.Domain(name, application)
			},
			List: getter.Domains,
		},
		{
			Type: "SmartOps",
			Get: func(name, application string) (pretty.PrettyResource, error) {
				return p.SmartOps(name, application)
			},
			List: getter.SmartOps,
		},
		{
			Type: "Functions",
			Get: func(name, application string) (pretty.PrettyResource, error) {
				return p.Function(name, application)
			},
			List: getter.Functions,
		},
	}
}

func (p *project) Prettify(prettifier pretty.Prettier) map[string]interface{} {
	getter := p.Get()
	obj := map[string]interface{}{
		"Id":          getter.Id(),
		"Name":        getter.Name(),
		"Description": getter.Description(),
		"Tags":        getter.Tags(),
		"Email":       getter.Email(),
	}
	var errors []error

	resourceFunctionSlice := p.ResourceMethods()

	apps := getter.Applications()
	appMap := map[string]interface{}{}
	for _, appName := range apps {
		app, err := p.Application(appName)
		if err != nil {
			errors = append(errors, fmt.Errorf("getting Application `%s` failed with: %s", appName, err))
			continue
		}

		appMap[appName] = app.Prettify(prettifier, resourceFunctionSlice)
	}

	if len(appMap) > 0 {
		obj["Applications"] = appMap
	}

	for _, resource := range resourceFunctionSlice {
		_, globalNames := resource.List("")
		resourceMap := map[string]interface{}{}

		for _, name := range globalNames {
			resourceObj, err := resource.Get(name, "")
			if err != nil {
				errors = append(errors, fmt.Errorf("getting %s `%s` failed with: %s", resource.Type, name, err))
				continue
			}

			resourceMap[name] = resourceObj.Prettify(prettifier)
		}

		if len(resourceMap) > 0 {
			obj[resource.Type] = resourceMap
		}
	}

	if len(errors) > 0 {
		obj["Errors"] = errors
	}

	return obj
}
