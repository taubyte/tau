package application_test

import (
	"errors"
	"testing"

	"github.com/taubyte/tau/pkg/schema/application"
	"github.com/taubyte/tau/pkg/schema/domains"
	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/pretty"
	"github.com/taubyte/tau/pkg/schema/services"
	"gotest.tools/v3/assert"
)

var (
	name        = "testApp"
	id          = "123456"
	description = "this is an app for testing"
	tags        = []string{"tag1", "tag2", "tag3"}
)

func TestPrettyBasic(t *testing.T) {
	project, err := internal.NewProjectEmpty()
	assert.NilError(t, err)

	app, err := project.Application(name)
	assert.NilError(t, err)

	err = app.Set(
		true,
		application.Id(id),
		application.Description(description),
		application.Tags(tags),
	)
	assert.NilError(t, err)

	assert.DeepEqual(t, app.Prettify(nil, project.ResourceMethods()), map[string]interface{}{
		"Id":          id,
		"Name":        name,
		"Description": description,
		"Tags":        tags,
	})

	var (
		serviceName        = "test_service"
		serviceId          = "472981"
		serviceDescription = "this is a test service"
		serviceProtocol    = "/test/v1"
		serviceTags        = []string{"tag_s_1", "tag_s_2"}
	)

	// Add a service
	service, err := project.Service(serviceName, name)
	assert.NilError(t, err)

	err = service.Set(true,
		services.Id(serviceId),
		services.Description(serviceDescription),
		services.Tags(serviceTags),
		services.Protocol(serviceProtocol),
	)
	assert.NilError(t, err)

	var (
		domainName        = "test_domain"
		domainId          = "419322"
		domainDescription = "this is a test domain"
		domainTags        = []string{"tag_d_1", "tag_d_2"}
		domainFQDN        = "hal.computers.com"
	)

	// Add a domain
	domain, err := project.Domain(domainName, name)
	assert.NilError(t, err)

	err = domain.Set(true,
		domains.Id(domainId),
		domains.Description(domainDescription),
		domains.Tags(domainTags),
		domains.FQDN(domainFQDN),
	)
	assert.NilError(t, err)

	assert.DeepEqual(t, app.Prettify(nil, project.ResourceMethods()), map[string]any{
		"Id":          id,
		"Name":        name,
		"Description": description,
		"Tags":        tags,
		"Services": map[string]any{
			serviceName: map[string]any{
				"Id":          serviceId,
				"Name":        serviceName,
				"Description": serviceDescription,
				"Tags":        serviceTags,
				"Protocol":    serviceProtocol,
			},
		},
		"Domains": map[string]any{
			domainName: map[string]any{
				"Id":             domainId,
				"Name":           domainName,
				"Description":    domainDescription,
				"Tags":           domainTags,
				"FQDN":           domainFQDN,
				"UseCertificate": false,
				"Type":           "",
			},
		},
	})
}
func TestPrettyErrors(t *testing.T) {
	project, err := internal.NewProjectEmpty()
	assert.NilError(t, err)

	app, err := project.Application(name)
	assert.NilError(t, err)

	err = app.Set(
		true,
		application.Id(id),
		application.Description(description),
		application.Tags(tags),
	)
	assert.NilError(t, err)

	pretty := app.Prettify(nil, []pretty.PrettyResourceIface{
		{
			Get: func(name string, application string) (pretty.PrettyResource, error) {
				return nil, errors.New("test error")
			},
			List: func(application string) ([]string, []string) {
				return []string{"broken_resource"}, nil
			},
		},
	})
	assert.ErrorContains(t, pretty["Errors"].([]error)[0], "getting testApp/broken_resource failed with: test error")

}
