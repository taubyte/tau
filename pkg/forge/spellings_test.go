package forge

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/git/repokey"
)

// speller is a forge serving https under /scm/ on its host and ssh on another
// port of it — the shape Spellings exists for.
type speller struct {
	plain
	hosts []string
	// wander maps to a key on another host, which Key must refuse.
	wander bool
}

func (d *speller) SpellingHosts() []string { return d.hosts }

func (d *speller) Canonical(k repokey.Key) (repokey.Key, bool) {
	if d.wander {
		return repokey.Key{Host: "elsewhere.invalid", Owner: k.Owner, Name: k.Name}, true
	}
	if k.Host != d.host {
		k.Host = d.host
		return k, true
	}
	if rest, ok := strings.CutPrefix(k.Owner, "scm/"); ok {
		k.Owner = rest
		return k, true
	}
	return k, false
}

func TestKeyNamesOneRepositoryForEverySpellingAForgeHandsOut(t *testing.T) {
	d := &speller{plain: plain{host: stubHost}, hosts: []string{stubHost + ":7999"}}
	r, err := New(Policy{}, Binding{Host: stubHost, Driver: d}, Binding{Host: "other.invalid", Driver: &plain{host: "other.invalid"}})
	require.NoError(t, err)

	want := key(t, "https://"+stubHost+"/proj/repo")
	for _, spelling := range []string{
		"https://" + stubHost + "/scm/PROJ/repo.git",
		"ssh://git@" + stubHost + ":7999/PROJ/repo.git",
		"https://" + stubHost + "/proj/repo",
	} {
		got, err := r.Key(spelling)
		require.NoError(t, err, spelling)
		assert.Equal(t, want, got, spelling)
	}

	// A host nobody spells, and a driver with no Spellings face, keep the parse.
	for _, spelling := range []string{"https://nobody.invalid/a/b", "https://other.invalid/scm/a/b"} {
		got, err := r.Key(spelling)
		require.NoError(t, err)
		assert.Equal(t, key(t, spelling), got)
	}
	// The spelling host binds no credential and is not listed.
	assert.Equal(t, []string{"git.example.invalid", "other.invalid"}, r.Hosts())
	_, err = r.Host(stubHost+":7999", Token("t"))
	assert.Error(t, err)

	_, err = r.Key("not a url")
	assert.Error(t, err)

	var none *Registry
	got, err := none.Key("https://" + stubHost + "/scm/proj/repo")
	require.NoError(t, err)
	assert.Equal(t, key(t, "https://"+stubHost+"/scm/proj/repo"), got, "a nil registry is repokey's parse")
}

func TestKeyRefusesASpellingOntoAnotherForge(t *testing.T) {
	d := &speller{plain: plain{host: stubHost}, wander: true}
	r, err := New(Policy{}, Binding{Host: stubHost, Driver: d})
	require.NoError(t, err)
	_, err = r.Key("https://" + stubHost + "/a/b")
	assert.ErrorContains(t, err, "elsewhere.invalid")
}

func TestNewRefusesSpellingHostsThatCollide(t *testing.T) {
	for name, bindings := range map[string][]Binding{
		"not a host spelling": {{Host: stubHost, Driver: &speller{plain: plain{host: stubHost}, hosts: []string{"Bad.Host"}}}},
		"a bound host": {
			{Host: stubHost, Driver: &speller{plain: plain{host: stubHost}, hosts: []string{"other.invalid"}}},
			{Host: "other.invalid", Driver: &plain{host: "other.invalid"}},
		},
		"claimed twice": {
			{Host: stubHost, Driver: &speller{plain: plain{host: stubHost}, hosts: []string{"ssh.invalid"}}},
			{Host: "other.invalid", Driver: &speller{plain: plain{host: "other.invalid"}, hosts: []string{"ssh.invalid"}}},
		},
	} {
		_, err := New(Policy{}, bindings...)
		assert.Error(t, err, name)
	}
}
