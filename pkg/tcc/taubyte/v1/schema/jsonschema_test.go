package schema

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// The exported JSON schema must carry every constraint KIND the DSL declares (not
// just be byte-stable against its golden): this guards intent, so a refactor that
// silently drops (say) x-tau-ref fails here even if the golden is regenerated.
func TestJSONSchemaCarriesConstraints(t *testing.T) {
	b, err := JSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Defs map[string]struct {
			Description string                     `json:"description"`
			Properties  map[string]json.RawMessage `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("emitted schema is not valid JSON: %v", err)
	}

	fn, ok := doc.Defs["Function"]
	if !ok {
		t.Fatal("no Function def")
	}
	if fn.Description == "" {
		t.Error("Function def has no description")
	}
	// enum: function type is a closed set.
	assertContains(t, fn.Properties["trigger"], `"type"`, `"enum"`, `"http"`, `"p2p"`)
	// intra-element shape + cross-element ref: source is "." or a library.
	assertContains(t, fn.Properties["source"], `"oneOf"`, `"const"`, `"libraries/"`, `"x-tau-ref"`)
	// cross-element ref: a function's domains must be defined domains.
	assertContains(t, fn.Properties["trigger"], `"x-tau-ref"`, `"domains"`)
	// per-field description is present.
	assertContains(t, fn.Properties["source"], `"description"`)
	// deferred external check surfaces on the domain fqdn.
	if dom, ok := doc.Defs["Domain"]; ok {
		assertContains(t, dom.Properties["fqdn"], `"x-tau-validation"`, `"dns"`, `"format"`)
	} else {
		t.Error("no Domain def")
	}
	// scalar authored form is documented.
	assertContains(t, fn.Properties["execution"], `"x-tau-scalar"`, `"duration"`)

	// display sections: a presentation overlay (schema-only). The resource carries
	// a section registry (id + title), and each field a section membership.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	fnDef := objectAt(t, raw["$defs"], "Function")
	assertContains(t, fnDef, `"x-tau-sections"`, `"trigger"`, `"code"`, `"limits"`, `"title"`)
	// "call" lives under the execution PATH but belongs to the "code" SECTION with
	// "source" — proving section membership is explicit, not derived from nesting.
	call := objectAt(t, raw["$defs"], "Function", "properties", "execution", "properties", "call")
	assertContains(t, call, `"x-tau-section"`, `"code"`)
	src := objectAt(t, raw["$defs"], "Function", "properties", "source")
	assertContains(t, src, `"x-tau-section"`, `"code"`)

	// static conditions: the HTTP section shows only when type is http/https, and a
	// field can carry its own condition too (cert data only for inline certs).
	assertContains(t, fnDef, `"show-when"`, `"http"`, `"https"`, `"field"`, `"type"`)
	certData := objectAt(t, raw["$defs"], "Domain", "properties", "certificate", "properties", "cert")
	assertContains(t, certData, `"x-tau-show-when"`, `"certificate-type"`, `"inline"`)

	// every resource + the container get a $def.
	for _, want := range []string{"Function", "Domain", "Library", "Website", "Database", "Storage", "Messaging", "Service", "SmartOp", "Application"} {
		if _, ok := doc.Defs[want]; !ok {
			t.Errorf("missing $def %q", want)
		}
	}
}

// Field order in the schema must follow DSL declaration order (JSON member order,
// which UIs render), not Go's alphabetical map marshaling — the whole point of the
// ordered emitter. We read the token stream because unmarshaling into a map would
// itself drop order.
func TestJSONSchemaFieldOrder(t *testing.T) {
	b, err := JSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}

	// Root: identity block leads, then resource maps in DSL order.
	assertOrder(t, orderedKeys(t, doc["properties"]),
		"id", "name", "description", "tags", "databases", "functions", "websites", "applications")

	fnProps := objectAt(t, doc["$defs"], "Function", "properties")
	// Function: identity block first (common-first), then its own fields in order.
	assertOrder(t, orderedKeys(t, fnProps),
		"id", "name", "description", "tags", "trigger", "source", "execution")
	// Nested authored objects keep DSL order too.
	assertOrder(t, orderedKeys(t, objectAt(t, fnProps, "trigger", "properties")),
		"type", "local", "channel", "command", "method", "domains", "paths")
	assertOrder(t, orderedKeys(t, objectAt(t, fnProps, "execution", "properties")),
		"timeout", "memory", "call")
}

// orderedKeys returns a JSON object's member keys in document order (json.Decoder
// preserves order; skipping each value as RawMessage keeps it shallow/robust).
func orderedKeys(t *testing.T, obj json.RawMessage) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(obj))
	if _, err := dec.Token(); err != nil { // opening '{'
		t.Fatalf("not an object: %v", err)
	}
	var keys []string
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, k.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

// objectAt descends a chain of object keys and returns the object at the end.
func objectAt(t *testing.T, obj json.RawMessage, path ...string) json.RawMessage {
	t.Helper()
	for _, k := range path {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(obj, &m); err != nil {
			t.Fatalf("unmarshal at %q: %v", k, err)
		}
		next, ok := m[k]
		if !ok {
			t.Fatalf("no key %q", k)
		}
		obj = next
	}
	return obj
}

// assertOrder checks that want appears as a subsequence of got (relative order).
func assertOrder(t *testing.T, got []string, want ...string) {
	t.Helper()
	pos := make(map[string]int, len(got))
	for i, k := range got {
		pos[k] = i
	}
	prev := -1
	for _, w := range want {
		i, ok := pos[w]
		if !ok {
			t.Errorf("key %q absent from %v", w, got)
			continue
		}
		if i < prev {
			t.Errorf("key %q out of order in %v", w, got)
		}
		prev = i
	}
}

func assertContains(t *testing.T, raw json.RawMessage, subs ...string) {
	t.Helper()
	s := string(raw)
	if s == "" {
		t.Errorf("property missing entirely (wanted %v)", subs)
		return
	}
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			t.Errorf("schema fragment %s\n  missing %q", s, sub)
		}
	}
}
