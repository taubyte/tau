package engine

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"

	mh "github.com/multiformats/go-multihash"
)

// Generated fields: values a consumer must MINT rather than ask the user for —
// a resource id today. The DSL says which fields those are and which known
// generator makes them, so tau-cli and the web console stop each carrying their
// own "how do I mint an id" (both currently do, and they disagree on the seed).

// Known generator ids. A generated field names one of these; a consumer that
// doesn't recognize the id treats the field as read-only and leaves it alone.
const (
	// GenCID mints a fresh CIDv0-shaped id: a base58 sha2-256 multihash over the
	// seed plus fresh randomness. Unique per call, and parses as a CID (see IsCID).
	GenCID = "cid"
)

// Generated declares that this attribute's value is minted by a known generator
// rather than authored — a UI renders it read-only and fills it via Generate.
// Schema/tooling only: no compile, parse or validation effect (the field keeps
// whatever validator it declares).
func Generated(kind string) Option { return Annotate("generated", kind) }

// GeneratedBy reports the generator id declared for a field of a resource group,
// or "" when the field is authored (or unknown).
func GeneratedBy(root []*Node, group string, field []string) string {
	a := findAttr(root, group, field)
	if a == nil {
		return ""
	}
	kind, _ := a.Meta["generated"].(string)
	return kind
}

// Generate mints a value for a generated field. seed is the caller's context
// (e.g. the resource's address); a generator MUST mix in its own entropy rather
// than rely on it, so two resources that share a seed still never collide and
// re-generating one never reproduces the old value.
func Generate(kind string, seed ...any) (string, error) {
	switch kind {
	case GenCID:
		return newCID(seed...)
	case "":
		return "", errors.New("not a generated field")
	}
	return "", fmt.Errorf("unknown generator %q", kind)
}

func newCID(seed ...any) (string, error) {
	r := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, r); err != nil {
		return "", fmt.Errorf("generating randomness failed: %w", err)
	}
	hash, err := mh.Sum(fmt.Append(nil, append(seed, r)...), mh.SHA2_256, -1)
	if err != nil {
		return "", err
	}
	return hash.B58String(), nil
}
