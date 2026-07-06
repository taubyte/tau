package gen

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v1/schema"
)

var specTypes = map[string]reflect.Type{
	"Function":  reflect.TypeFor[structureSpec.Function](),
	"Database":  reflect.TypeFor[structureSpec.Database](),
	"Domain":    reflect.TypeFor[structureSpec.Domain](),
	"Library":   reflect.TypeFor[structureSpec.Library](),
	"Messaging": reflect.TypeFor[structureSpec.Messaging](),
	"Service":   reflect.TypeFor[structureSpec.Service](),
	"SmartOp":   reflect.TypeFor[structureSpec.SmartOp](),
	"Storage":   reflect.TypeFor[structureSpec.Storage](),
	"Website":   reflect.TypeFor[structureSpec.Website](),
}

// TestStructParity asserts every field tcc-gen generates matches the hand-written
// structureSpec struct exactly — name, Go type, and mapstructure tag. A mismatch
// means the DSL (or the generator) is wrong. Real fields the generator does NOT
// emit are the hand-written tail (value transforms, derived fields like
// Function.Secure, embedded markers) and are reported, not failed.
func TestStructParity(t *testing.T) {
	models, err := Structs(schema.TaubyteRessources)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range models {
		rt, ok := specTypes[m.Spec]
		if !ok {
			t.Fatalf("no real struct registered for %s", m.Spec)
		}
		real := realFields(rt)

		for _, f := range m.Fields {
			rf, ok := real[f.Name]
			if !ok {
				t.Errorf("%s: generated field %q has no match in the hand-written struct", m.Spec, f.Name)
				continue
			}
			if rf.goType != f.Type {
				t.Errorf("%s.%s: type gen=%q real=%q", m.Spec, f.Name, f.Type, rf.goType)
			}
			if rf.tag != genTagValue(f.Tag) {
				t.Errorf("%s.%s: mapstructure tag gen=%q real=%q", m.Spec, f.Name, genTagValue(f.Tag), rf.tag)
			}
			delete(real, f.Name)
		}

		tail := make([]string, 0, len(real))
		for name := range real {
			tail = append(tail, name)
		}
		sort.Strings(tail)
		if len(tail) > 0 {
			t.Logf("%s: %d/%d fields generated; hand-written tail: %v",
				m.Spec, len(m.Fields), len(m.Fields)+len(tail), tail)
		}
	}
}

type fieldInfo struct{ goType, tag string }

func realFields(rt reflect.Type) map[string]fieldInfo {
	m := make(map[string]fieldInfo, rt.NumField())
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if f.Anonymous {
			continue // embedded markers (Wasm/Basic/Indexer) — not data fields
		}
		m[f.Name] = fieldInfo{goType: f.Type.String(), tag: f.Tag.Get("mapstructure")}
	}
	return m
}

// genTagValue extracts the mapstructure value from a generated tag literal like
// `mapstructure:"service"` (with backticks), or "" when there is no tag.
func genTagValue(t string) string {
	return reflect.StructTag(strings.Trim(t, "`")).Get("mapstructure")
}
