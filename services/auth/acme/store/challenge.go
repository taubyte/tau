package store

import (
	"strconv"
	"strings"
	"time"
)

// ChallengeKey is the store key of the pending DNS-01 TXT value for _acme-challenge.<fqdn>.
// The +token suffix keeps it in the ACME cache namespace, not with the certificates.
func ChallengeKey(fqdn string) string { return "_acme-challenge." + fqdn + "+token" }

// EncodeChallenge stores a value that counts as pending until `until`.
// The store cannot delete cache entries, so clearing writes a past `until`.
func EncodeChallenge(txt string, until time.Time) []byte {
	return []byte(txt + " " + strconv.FormatInt(until.Unix(), 10))
}

// DecodeChallenge returns the value if it is still pending.
func DecodeChallenge(v []byte) (string, bool) {
	txt, until, ok := strings.Cut(string(v), " ")
	if !ok || txt == "" {
		return "", false
	}
	u, err := strconv.ParseInt(until, 10, 64)
	if err != nil || time.Now().Unix() >= u {
		return "", false
	}
	return txt, true
}

// Lookup reads a cache entry straight from auth: no retry, no local copy,
// so a value that changes (a lock, a challenge) is never read stale.
func (d *Store) Lookup(name string) ([]byte, error) {
	return d.getDynamicCertificate(name, false)
}
