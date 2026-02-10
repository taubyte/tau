package templates

import (
	"os"
	"path"

	"gopkg.in/yaml.v3"
)

func (t *templates) Websites() (map[string]TemplateInfo, error) {
	return t.genericRepositoryTemplates(templateWebsiteFolder)
}

func (t *templates) Libraries() (map[string]TemplateInfo, error) {
	return t.genericRepositoryTemplates(templateLibraryFolder)
}

func (t *templates) genericRepositoryTemplates(folder string) (map[string]TemplateInfo, error) {
	templates, err := os.ReadDir(folder)
	if err != nil {
		return nil, err
	}

	templateMap := make(map[string]TemplateInfo, len(templates))
	for _, template := range templates {
		templateDir := path.Join(folder, template.Name())

		yamlData, err := os.ReadFile(path.Join(templateDir, "config.yaml"))
		if err != nil {
			return nil, err
		}

		var _yaml templateYaml
		err = yaml.Unmarshal(yamlData, &_yaml)
		if err != nil {
			return nil, err
		}

		// TODO Update website template description style
		// description, err := os.ReadFile(path.Join(templateDir, "description.md"))
		// if err != nil {
		// 	return nil, err
		// }

		// stringDescription := string(description)

		// // remove trailing newlines
		// for {
		// 	if len(stringDescription) == 0 {
		// 		break
		// 	}

		// 	if stringDescription[len(stringDescription)-1] == '\n' {
		// 		stringDescription = stringDescription[:len(stringDescription)-1]
		// 	} else {
		// 		break
		// 	}
		// }

		templateMap[_yaml.Name] = TemplateInfo{
			URL: _yaml.URL,
			// Description: stringDescription,
		}
	}

	return templateMap, nil
}
