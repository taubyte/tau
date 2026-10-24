package jsonschema_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gjs "github.com/google/jsonschema-go/jsonschema"
	v2 "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gopkg.in/yaml.v3"
	"gotest.tools/v3/assert"
)

const corpus = "../../../node/dream/projects"

func published(t *testing.T) *gjs.Resolved {
	t.Helper()
	b, err := v2.JSONSchema()
	assert.NilError(t, err)
	var s gjs.Schema
	assert.NilError(t, json.Unmarshal(b, &s))
	r, err := s.Resolve(nil)
	assert.NilError(t, err)
	return r
}

func readYAML(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	assert.NilError(t, err)
	var v any
	assert.NilError(t, yaml.Unmarshal(b, &v))
	j, err := json.Marshal(v)
	assert.NilError(t, err)
	var m map[string]any
	assert.NilError(t, json.Unmarshal(j, &m))
	return m
}

// scope reads a scope directory as the published document shapes it: the scope's
// own fields, and <kind>/<name>.yaml as doc[kind][name]. A <kind>/<name>/ directory
// is a nested scope whose own fields sit in meta.yaml.
func scope(t *testing.T, dir string, doc map[string]any) map[string]any {
	t.Helper()
	kinds, err := os.ReadDir(dir)
	assert.NilError(t, err)
	for _, k := range kinds {
		if !k.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(dir, k.Name()))
		assert.NilError(t, err)
		group := map[string]any{}
		for _, e := range entries {
			p := filepath.Join(dir, k.Name(), e.Name())
			if e.IsDir() {
				group[e.Name()] = scope(t, p, readYAML(t, filepath.Join(p, "meta.yaml")))
				continue
			}
			group[strings.TrimSuffix(e.Name(), ".yaml")] = readYAML(t, p)
		}
		doc[k.Name()] = group
	}
	return doc
}

func projects(t *testing.T, under string) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	err := filepath.WalkDir(filepath.Join(corpus, under), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "project.yaml" {
			return err
		}
		dir := filepath.Dir(p)
		rel, err := filepath.Rel(corpus, dir)
		if err != nil {
			return err
		}
		out[rel] = scope(t, dir, readYAML(t, p))
		return nil
	})
	assert.NilError(t, err)
	assert.Assert(t, len(out) > 0)
	return out
}

func TestCorpusValidatesAgainstThePublishedSchema(t *testing.T) {
	r := published(t)
	for name, doc := range projects(t, "with") {
		t.Run(name, func(t *testing.T) {
			assert.NilError(t, r.Validate(doc))
		})
	}
}

func TestCorpusWithoutEnvironmentsIsRefused(t *testing.T) {
	r := published(t)
	doc := projects(t, "refused/environments")["refused/environments/missing"]
	assert.Assert(t, doc != nil)
	err := r.Validate(doc)
	assert.ErrorContains(t, err, "environments")

	doc["environments"] = map[string]any{}
	assert.ErrorContains(t, r.Validate(doc), "minProperties")
}

// A FUNCTION WITHOUT A TIER IT RUNS ON is refused by the published schema as it is by
// the compiler, so an editor says so before a build does.
func TestCorpusFunctionWithoutATierItRunsOnIsRefused(t *testing.T) {
	r := published(t)
	docs := projects(t, "refused/function")

	missing := docs["refused/function/noruntime"]
	assert.Assert(t, missing != nil)
	assert.ErrorContains(t, r.Validate(missing), `missing properties: ["runtime"]`)

	unknown := docs["refused/function/unknownruntime"]
	assert.Assert(t, unknown != nil)
	assert.ErrorContains(t, r.Validate(unknown), "properties/runtime: enum: nodejs does not equal any of: [wasm js]")
}
