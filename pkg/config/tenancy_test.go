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
	for _, url := range []string{"https://github.com/acme/widget", "git@github.com:acme/widget", ""} {
		if tn.OwnsCloneURL(url) {
			t.Fatalf("unconfigured tenancy claimed %q", url)
		}
	}
}

// The URL form has to answer what the full-name form answers, in every spelling
// of the URL — that is the whole reason the key is derived rather than the string
// pattern-matched. A subgroup belongs to its GROUP, because the group is what a
// cloud is scoped to.
func TestTenancyOwnsCloneURL(t *testing.T) {
	tn := Tenancy{Provider: "github", Owner: "Acme"}

	for _, url := range []string{
		"https://github.com/acme/widget",
		"https://github.com/Acme/Widget.git",
		"https://github.com:443/acme/widget/",
		"ssh://git@github.com/acme/widget",
		"ssh://git@github.com:22/acme/widget",
		"git@github.com:acme/widget",
		"git@github.com:ACME/widget.git",
		// another provider, same namespace: nothing here maps a provider name to
		// a host, so this answers the same as the full-name form always has
		"https://gitlab.com/acme/widget",
		// a subgroup under the owning group
		"https://gitlab.com/acme/team/widget",
	} {
		if !tn.OwnsCloneURL(url) {
			t.Fatalf("%q should belong to %q", url, tn.Owner)
		}
	}

	for _, url := range []string{
		"https://github.com/other/widget",
		// a namespace merely starting with the owner
		"https://github.com/acmeco/widget",
		// the owner as the repository name rather than the namespace
		"https://github.com/other/acme",
		// a bare repository at a host root has no namespace to own
		"https://git.internal/widget",
		// not clone URLs at all, and one that hides the namespace behind a
		// credential the key refuses outright
		"acme/widget", "", "not a url",
		"https://acme:token@github.com/acme/widget",
	} {
		if tn.OwnsCloneURL(url) {
			t.Fatalf("%q should not belong to %q", url, tn.Owner)
		}
	}
}
