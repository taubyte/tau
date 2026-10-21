package cli

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDomainClaimPrintsTheTXTRecord(t *testing.T) {
	x := newTester(t)
	x.login()
	x.link("proj-1")

	out := x.ok("domain", "claim", "Shop.Example.com.", "--dir", x.dir)
	require.Contains(t, out, "proj-1xx.shop.example.com")
	require.Contains(t, out, "TXT")
	require.Contains(t, out, "signed")

	var c map[string]string
	require.NoError(t, json.Unmarshal([]byte(x.ok("domain", "claim", "shop.example.com", "--dir", x.dir, "-o", "json")), &c))
	require.Equal(t, map[string]string{"entry": "proj-1xx.shop.example.com", "type": "txt", "token": "signed"}, c)

	require.Contains(t, x.run("domain", "claim", "--dir", x.dir).stderr, "a domain is required")
	require.Contains(t, x.run("domain", "claim", "shop.example.com", "--project", "proj-other").stderr, "not found")
	require.Contains(t, x.run("domain", "claim", "shop.example.com", "--dir", t.TempDir()).stderr, "not linked")
	require.NotZero(t, x.run("domain", "claim", "shop.example.com", "--project", "proj-1", "--cloud", "::").code)
}
