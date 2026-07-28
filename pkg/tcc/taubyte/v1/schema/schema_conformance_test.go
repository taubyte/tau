package schema

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"gotest.tools/v3/assert"
)

// The exported JSON Schema is consumed by editors and agents through ordinary
// validators, which the Go tests never exercise — so a schema can be structurally
// wrong while every Go test passes. It was: `required` listed nested fields by
// their bare name at the object root, and 14 of the fixtures below failed
// against it while the compiler happily accepted them.
//
// This walks the fixtures the compiler DOES accept and checks the schema accepts
// them too, on the one keyword that has broken: required, at every level.
func TestFixturesSatisfyTheExportedSchemaRequired(t *testing.T) {
	raw, err := JSONSchema()
	assert.NilError(t, err)

	var doc struct {
		Props map[string]schemaNode `json:"properties"`
		Defs  map[string]schemaNode `json:"$defs"`
	}
	assert.NilError(t, json.Unmarshal(raw, &doc))

	// group directory -> the $def its instances follow
	group2def := map[string]string{}
	for group, p := range doc.Props {
		if ref := p.AdditionalProperties.Ref; ref != "" {
			group2def[group] = strings.TrimPrefix(ref, "#/$defs/")
		}
	}
	assert.Assert(t, len(group2def) > 0, "schema exposes no resource groups")

	fixtures := filepath.Join("..", "fixtures", "config")
	checked := 0
	assert.NilError(t, filepath.Walk(fixtures, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".yaml") {
			return nil
		}
		rel, _ := filepath.Rel(fixtures, p)
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) < 2 {
			return nil
		}
		def, ok := group2def[parts[len(parts)-2]]
		if !ok {
			return nil
		}
		body, err := os.ReadFile(p)
		assert.NilError(t, err)
		var v map[string]any
		assert.NilError(t, yaml.Unmarshal(body, &v))
		checked++
		missing := missingRequired(v, doc.Defs[def], "")
		assert.Assert(t, len(missing) == 0,
			"%s compiles, but the exported schema calls it invalid: missing %v", rel, missing)
		return nil
	}))
	assert.Assert(t, checked >= 15, "expected to check a meaningful number of fixtures, got %d", checked)
}

type schemaNode struct {
	Required             []string              `json:"required"`
	Properties           map[string]schemaNode `json:"properties"`
	AdditionalProperties struct {
		Ref string `json:"$ref"`
	} `json:"additionalProperties"`
}

// missingRequired reports required properties the document lacks, at the level
// each is declared — the check a stock validator performs.
func missingRequired(doc map[string]any, sch schemaNode, path string) []string {
	var out []string
	at := func(k string) string {
		if path == "" {
			return k
		}
		return path + "/" + k
	}
	for _, r := range sch.Required {
		if _, ok := doc[r]; !ok {
			out = append(out, at(r))
		}
	}
	for k, sub := range sch.Properties {
		if len(sub.Properties) == 0 && len(sub.Required) == 0 {
			continue
		}
		if nested, ok := doc[k].(map[string]any); ok {
			out = append(out, missingRequired(nested, sub, at(k))...)
		}
	}
	return out
}
