package domains

import "github.com/taubyte/tau/pkg/schema/basic"

func Yaml(name, application string, yamlData []byte) (Getter, error) {
	resource, err := basic.Yaml(yamlData)
	if err != nil {
		return nil, err
	}

	return getter{
		&domain{
			Resource:    resource,
			name:        name,
			application: application,
		},
	}, nil
}
