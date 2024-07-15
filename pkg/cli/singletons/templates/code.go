package templates

import (
	"os"
	"path"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type codeConfig struct {
	Description string
}

func (t *templates) Function(language string) (map[string]TemplateInfo, error) {
	return t.codeMap(path.Join(templateFunctionsFolder, language))
}

func (t *templates) SmartOps(language string) (map[string]TemplateInfo, error) {
	return t.codeMap(path.Join(templateSmartOpsFolder, language))
}

func (t *templates) codeMap(templatePath string) (map[string]TemplateInfo, error) {
	dirs, err := os.ReadDir(templatePath)
	if err != nil {
		return nil, err
	}

	templateMap := make(map[string]TemplateInfo, len(dirs))
	for _, dir := range dirs {
		if dir.Name() == "common" {
			continue
		}
		yamlData, err := os.ReadFile(path.Join(templatePath, dir.Name(), "config.yaml"))
		if err != nil {
			return nil, err
		}

		var config codeConfig
		err = yaml.Unmarshal(yamlData, &config)
		if err != nil {
			return nil, err
		}

		templateMap[filepath.Base(dir.Name())] = TemplateInfo{
			HideURL:     true,
			URL:         path.Join(templatePath, dir.Name()),
			Description: config.Description,
		}
	}

	return templateMap, nil
}
