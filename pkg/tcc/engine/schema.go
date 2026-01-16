package engine

import (
	"encoding/json"

	"gopkg.in/yaml.v3"
)

func (s *schemaDef) Yaml() (string, error) {
	out, err := yaml.Marshal(s.Map())
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (s *schemaDef) Json() (string, error) {
	out, err := json.Marshal(s.Map())
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (s *schemaDef) Map() map[string]any {
	return map[string]any{
		"root": s.root.Map(),
	}
}

func SchemaDefinition(root *Node) Schema {
	return &schemaDef{
		root: root,
	}
}
