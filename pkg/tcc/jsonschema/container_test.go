package jsonschema

import (
	"encoding/json"
	"testing"

	engine "github.com/taubyte/tau/pkg/tcc/engine"
	"gotest.tools/v3/assert"
)

func containerRoot(singular bool) []*engine.Node {
	scope := engine.DefineIterGroup(
		[]*engine.Attribute{engine.String("id", engine.Required())},
		engine.DefineGroup("things", engine.DefineIter(
			[]*engine.Attribute{engine.String("id")},
			engine.Resource("things", "Thing", "Thing", "thing"),
		)),
	)
	if singular {
		scope = scope.With(engine.Singular("Scope"))
	}
	return []*engine.Node{
		engine.DefineGroup("things", engine.DefineIter(
			[]*engine.Attribute{
				engine.String("id", engine.Required()),
				engine.String("label", engine.Derived(engine.DeriveDNSLabel, "id")),
				engine.String("on", engine.Path("source", engine.Either("git", "path")), engine.Captures()),
				engine.Bool("flag", engine.Path("source")),
			},
			engine.Resource("things", "Thing", "Thing", "thing"),
		)),
		engine.DefineGroup("scopes", scope),
		engine.DefineGroup("clouds", engine.DefineIter([]*engine.Attribute{engine.String("v")})),
	}
}

func TestAContainerIsADefOfItsOwn(t *testing.T) {
	raw, err := GenerateJSONSchema(containerRoot(true), JSONSchemaOptions{ID: "https://example.test/s"})
	assert.NilError(t, err)
	var doc struct {
		ID         string                     `json:"$id"`
		Defs       map[string]json.RawMessage `json:"$defs"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	assert.NilError(t, json.Unmarshal(raw, &doc))
	assert.Equal(t, doc.ID, "https://example.test/s")

	var scope struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	assert.NilError(t, json.Unmarshal(doc.Defs["Scope"], &scope))
	var held map[string]any
	assert.NilError(t, json.Unmarshal(scope.Properties["things"], &held))
	assert.DeepEqual(t, held["additionalProperties"], map[string]any{"$ref": "#/$defs/Thing"})
	_, isDef := doc.Defs["clouds"]
	assert.Assert(t, !isDef, "a leaf map decodes to no type")
	_, listed := doc.Properties["clouds"]
	assert.Assert(t, !listed)

	_, err = GenerateJSONSchema(containerRoot(false), JSONSchemaOptions{})
	assert.ErrorContains(t, err, `container group "scopes" has no Singular() declaration`)
}

func TestDerivedFieldsAndBareValuesArePublished(t *testing.T) {
	raw, err := GenerateJSONSchema(containerRoot(true), JSONSchemaOptions{ExtPrefix: "x-t-"})
	assert.NilError(t, err)
	var doc struct {
		Defs map[string]struct {
			Properties map[string]map[string]any `json:"properties"`
		} `json:"$defs"`
	}
	assert.NilError(t, json.Unmarshal(raw, &doc))
	label := doc.Defs["Thing"].Properties["label"]
	assert.Equal(t, label["x-t-derived"], engine.DeriveDNSLabel)
	assert.DeepEqual(t, label["x-t-derived-from"], []any{"id"})

	for typ, attr := range map[string]*engine.Attribute{
		"boolean": engine.Bool("v", engine.Path("source")),
		"integer": engine.Int("v", engine.Path("source")),
		"number":  engine.Float("v", engine.Path("source")),
		"array":   engine.StringSlice("v", engine.Path("source")),
		"string":  engine.String("v", engine.Path("source")),
	} {
		iter := engine.DefineIter([]*engine.Attribute{engine.String("k", engine.Path("source", engine.Either("git")), engine.Captures()), attr})
		assert.Equal(t, bareValueType(iter, []string{"source"}), typ)
	}
	none := engine.DefineIter([]*engine.Attribute{engine.String("v", engine.Path("other"))})
	assert.Equal(t, bareValueType(none, []string{"source"}), "")
	assert.Equal(t, bareValueType(none, nil), "")
}
