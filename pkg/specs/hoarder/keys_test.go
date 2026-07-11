package hoarder

import "testing"

func TestKeyHelpers(t *testing.T) {
	const hash = "QmHash"
	const pid = "12D3KooWpeer"
	const cid = "bafyCid"

	cases := map[string]string{
		MetaKey(hash):           "/hoarder/meta/QmHash",
		ClaimsPathOf(hash):      "/hoarder/claims/QmHash/",
		ClaimKey(hash, pid):     "/hoarder/claims/QmHash/12D3KooWpeer",
		StashMetaKey(cid):       "/hoarder/stash/meta/bafyCid",
		StashClaimsPathOf(cid):  "/hoarder/stash/claims/bafyCid/",
		StashClaimKey(cid, pid): "/hoarder/stash/claims/bafyCid/12D3KooWpeer",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("key mismatch: got %q, want %q", got, want)
		}
	}

	// A claim key must sit under its claims prefix — the repair loop lists by
	// prefix, so this relationship is load-bearing.
	if got := ClaimKey(hash, pid); got[:len(ClaimsPathOf(hash))] != ClaimsPathOf(hash) {
		t.Errorf("ClaimKey %q not under ClaimsPathOf %q", got, ClaimsPathOf(hash))
	}
}
