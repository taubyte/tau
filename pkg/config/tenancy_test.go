package config

import "testing"

func TestTenancyConfigured(t *testing.T) {
	if (Tenancy{}).Configured() {
		t.Fatal("empty tenancy reported as configured")
	}
	if !(Tenancy{Owner: "acme"}).Configured() {
		t.Fatal("tenancy with an owner reported as unconfigured")
	}
}

func TestTenancyOwns(t *testing.T) {
	tn := Tenancy{Provider: "github", Owner: "Acme"}

	for _, fullName := range []string{"Acme/widget", "acme/widget", "ACME/widget"} {
		if !tn.Owns(fullName) {
			t.Fatalf("%q should belong to %q — provider namespaces are case-insensitive", fullName, tn.Owner)
		}
	}

	// A repo under a different namespace, one whose name merely starts with the
	// owner, and inputs with no owner segment at all must all be refused.
	for _, fullName := range []string{"other/widget", "acmeco/widget", "widget", "", "/widget"} {
		if tn.Owns(fullName) {
			t.Fatalf("%q should not belong to %q", fullName, tn.Owner)
		}
	}
}

// An unconfigured tenancy must not accidentally match a repository whose owner
// segment is empty.
func TestTenancyUnconfiguredOwnsNothing(t *testing.T) {
	var tn Tenancy
	for _, fullName := range []string{"acme/widget", "/widget", "", "widget"} {
		if tn.Owns(fullName) {
			t.Fatalf("unconfigured tenancy claimed %q", fullName)
		}
	}
}
