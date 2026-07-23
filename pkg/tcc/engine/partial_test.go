package engine

import "testing"

// A tiny DSL: one resource "widgets" with an enum field kind and a plain field
// note (no validator).
func partialRoot() []*Node {
	return []*Node{
		DefineGroup("widgets", DefineIter([]*Attribute{
			String("kind", Path("spec", "kind"), InSet("a", "b", "c")),
			String("note"),
		})),
	}
}

func TestValidateField(t *testing.T) {
	root := partialRoot()

	if err := ValidateField(root, "widgets", []string{"spec", "kind"}, "b"); err != nil {
		t.Errorf("valid enum value should pass: %v", err)
	}
	if err := ValidateField(root, "widgets", []string{"spec", "kind"}, "z"); err == nil {
		t.Error("invalid enum value should fail")
	}
	// a known field with no validator is valid (unconstrained), not an error
	if err := ValidateField(root, "widgets", []string{"note"}, "anything"); err != nil {
		t.Errorf("unvalidated known field should pass: %v", err)
	}
	// but an unknown field path / group is reported as unknown, not silently OK
	if err := ValidateField(root, "widgets", []string{"nope"}, "x"); err == nil {
		t.Error("unknown field should error")
	}
	if err := ValidateField(root, "ghost", []string{"kind"}, "z"); err == nil {
		t.Error("unknown group should error")
	}
}

func TestValidatedFields(t *testing.T) {
	got := ValidatedFields(partialRoot(), "widgets")
	if len(got) != 1 {
		t.Fatalf("want 1 validated field (kind), got %d", len(got))
	}
	if len(got[0].Path) != 2 || got[0].Path[0] != "spec" || got[0].Path[1] != "kind" {
		t.Errorf("unexpected path %v", got[0].Path)
	}
	if got[0].Validate("z") == nil {
		t.Error("returned validator should reject an out-of-set value")
	}
}
