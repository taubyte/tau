package engine

import (
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

// Derive is a pure function: same input, same output, on every machine, forever.
// Generate is the opposite by contract, and confusing the two is a real bug in either
// direction — a minted id that reproduced would collide, a derived name that did not
// would resolve differently in two consumers.
func TestDeriveIsPureAndGenerateIsNot(t *testing.T) {
	const id = "QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR"

	a, err := Derive(DeriveDNSLabel, id)
	assert.NilError(t, err)
	b, err := Derive(DeriveDNSLabel, id)
	assert.NilError(t, err)
	assert.Equal(t, a, b, "a derivation that is not reproducible is not a derivation")

	g1, err := Generate(GenCID, id)
	assert.NilError(t, err)
	g2, err := Generate(GenCID, id)
	assert.NilError(t, err)
	assert.Assert(t, g1 != g2, "a generator that reproduces its output would mint colliding ids")
}

// A DNS label is case-insensitive, so the alphabet has to be too, and it may hold
// only letters and digits.
func TestDeriveDNSLabelShape(t *testing.T) {
	label, err := Derive(DeriveDNSLabel, "QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR")
	assert.NilError(t, err)
	assert.Equal(t, len(label), dnsLabelLength)
	assert.Equal(t, label, strings.ToLower(label), "case cannot carry information in DNS")
	for _, r := range label {
		ok := (r >= 'a' && r <= 'z') || (r >= '2' && r <= '7')
		assert.Assert(t, ok, "label %q has %q, which base32 does not produce", label, r)
	}

	_, err = Derive(DeriveDNSLabel)
	assert.ErrorContains(t, err, "needs an identifier")
	_, err = Derive("no-such-derivation", "x")
	assert.ErrorContains(t, err, "unknown derivation")
}
