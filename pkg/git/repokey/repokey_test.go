package repokey_test

import (
	"strings"
	"testing"

	"github.com/taubyte/tau/pkg/git/repokey"
)

func TestEverySpellingOfOneRepositoryIsOneKey(t *testing.T) {
	for want, spellings := range map[string][]string{
		"github.com/acme/pay": {
			"https://github.com/acme/pay",
			"https://github.com/acme/pay.git",
			"https://github.com/acme/pay/",
			"https://github.com/Acme/Pay.GIT",
			"HTTPS://GitHub.COM/acme/pay",
			"https://github.com:443/acme/pay",
			"https://git@github.com/acme/pay",
			"http://github.com:80/acme/pay",
			// the pair the package had to decide
			"ssh://git@github.com/acme/pay",
			"ssh://git@github.com:22/acme/pay",
			"git@github.com:acme/pay",
			"git@github.com:/acme/pay",
			"git@github.com:acme/pay.git",
			"git://github.com:9418/acme/pay",
		},
		// A GitLab subgroup nests, so the owner is the whole path above the
		// repository and not just the group.
		"gitlab.com/acme/team/widget": {
			"https://gitlab.com/acme/team/widget",
			"git@gitlab.com:acme/team/widget.git",
			"ssh://git@gitlab.com/acme/team/widget",
		},
		// A self-hosted host on a non-default port keeps it: that is a different
		// server, not a different spelling.
		"git.acme.internal:2222/team/repo": {
			"ssh://git@git.acme.internal:2222/team/repo",
			"ssh://git.acme.internal:2222/team/repo.git",
		},
		// A bare repository at a host's root has no owner.
		"g/x": {"https://g/x", "https://g/x.git", "git@g:x"},
	} {
		for _, s := range spellings {
			got, err := repokey.String(s)
			if err != nil {
				t.Fatalf("%q: %v", s, err)
			}
			if got != want {
				t.Fatalf("%q keyed as %q, want %q", s, got, want)
			}
		}
	}
}

func TestScpFormHasNoPort(t *testing.T) {
	k, err := repokey.Parse("git@github.com:22/acme/pay")
	if err != nil {
		t.Fatal(err)
	}
	if k.String() != "github.com/22/acme/pay" {
		t.Fatalf("scp form read a port: %q", k)
	}
	if u, err := repokey.String("ssh://git@github.com:22/acme/pay"); err != nil || u != "github.com/acme/pay" {
		t.Fatalf("ssh form: %q %v", u, err)
	}
}

func TestParts(t *testing.T) {
	type testCase struct {
		url, host, owner, name, full, ns string
	}
	for _, tc := range []testCase{
		{"https://github.com/Acme/Pay.git", "github.com", "acme", "pay", "acme/pay", "acme"},
		{"git@gitlab.com:acme/team/widget", "gitlab.com", "acme/team", "widget", "acme/team/widget", "acme"},
		{"https://g/x", "g", "", "x", "x", ""},
		{"ssh://git@h:2222/a/b", "h:2222", "a", "b", "a/b", "a"},
		{"http://[::1]/a/b", "[::1]", "a", "b", "a/b", "a"},
	} {
		k, err := repokey.Parse(tc.url)
		if err != nil {
			t.Fatalf("%q: %v", tc.url, err)
		}
		if k.Host != tc.host || k.Owner != tc.owner || k.Name != tc.name {
			t.Fatalf("%q -> %+v, want host=%q owner=%q name=%q", tc.url, k, tc.host, tc.owner, tc.name)
		}
		if k.FullName() != tc.full {
			t.Fatalf("%q full name %q, want %q", tc.url, k.FullName(), tc.full)
		}
		if k.Namespace() != tc.ns {
			t.Fatalf("%q namespace %q, want %q", tc.url, k.Namespace(), tc.ns)
		}
	}
}

func TestRefusals(t *testing.T) {
	type testCase struct{ url, contains string }
	for _, tc := range []testCase{
		{"", "names no repository"},
		{"acme/pay", "no scheme"},
		{"https://u:ghp_secret@github.com/a/b", "carries a credential"},
		{"ghp_x:y@github.com:a/b", "carries a credential"},
		{"git@github.com:acme/re po", "not a URL"},
		{"https://github.com/acme/re po", "whitespace"},
		{"git@github.com:/", "no repository"},
		{"https://github.com", "no repository"},
		{"https://github.com/", "no repository"},
		{"https://github.com/acme/.git", "no repository"},
		{"https://:8080/x", "names no host"},
		{"file:///srv/repo", "not a git transport"},
		{"local://x/y", "not a git transport"},
		{"https://github.com/a//b", "empty path segment"},
		{"https://github.com/a/../b", "does not climb"},
		{"https://github.com/./b", "does not climb"},
	} {
		err := repokey.Valid(tc.url)
		if err == nil {
			t.Fatalf("%q was accepted", tc.url)
		}
		if !strings.Contains(err.Error(), tc.contains) {
			t.Fatalf("%q: error %q does not mention %q", tc.url, err, tc.contains)
		}
	}
}

func TestValidAndParseAreOneGrammar(t *testing.T) {
	for _, s := range []string{
		"", "https://github.com/acme/pay", "git@github.com:acme/pay", "acme/pay",
		"https://u:p@h/a/b", "ssh://git@h:22/a/b", "file:///x", "https://h/a//b",
		"https://h/", "git@h:/", "https://h/x.git", "not a url at all",
	} {
		_, perr := repokey.Parse(s)
		verr := repokey.Valid(s)
		if (perr == nil) != (verr == nil) {
			t.Fatalf("%q: Parse=%v Valid=%v", s, perr, verr)
		}
	}
}

func TestKeyIsStableUnderReDerivation(t *testing.T) {
	for _, s := range []string{
		"git@github.com:Acme/Pay.git",
		"ssh://git@gitlab.com:22/acme/team/widget",
		"https://github.com:443/acme/pay/",
	} {
		first, err := repokey.String(s)
		if err != nil {
			t.Fatal(err)
		}
		again, err := repokey.String("https://" + first)
		if err != nil {
			t.Fatal(err)
		}
		if again != first {
			t.Fatalf("%q keyed %q, re-deriving gave %q", s, first, again)
		}
	}
}

func TestAKeyIsStoredUnderOneSpelling(t *testing.T) {
	for _, spelt := range []string{"github.com/acme/shop", " GitHub.com/Acme/Shop/ ", "/github.com/ACME/shop"} {
		if got := repokey.Normalise(spelt); got != "github.com/acme/shop" {
			t.Fatalf("Normalise(%q) = %q", spelt, got)
		}
	}
	if got := repokey.Normalise("  /  "); got != "" {
		t.Fatalf("a key of nothing normalises to %q", got)
	}
}
