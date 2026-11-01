package bitbucketserver

import (
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestARequestIsItsRoute(t *testing.T) {
	_, err := routeRepository.at("ACME").path()
	assert.ErrorContains(t, err, "takes 2 arguments and was given 1")
	_, err = routeRepository.at("ACME", "pay", "extra").path()
	assert.ErrorContains(t, err, "was given 3")
	_, err = routeRepository.at("ACME", "..").path()
	assert.ErrorContains(t, err, "is not a name")

	_, err = routeRepository.at("ACME", "pay").with(url.Values{"start": {"0"}}).rawQuery()
	assert.ErrorContains(t, err, "does not declare")

	c := (&driver{host: "bb.example.com", base: &url.URL{Scheme: "https", Host: "bb.example.com", Path: "/"}}).bind("t")
	assert.Error(t, c.send(t.Context(), routeRepository.at("ACME"), nil, nil))
	assert.Error(t, c.send(t.Context(), routeRepository.at("ACME", "pay").with(url.Values{"x": {"1"}}), nil, nil))
	assert.Error(t, c.send(t.Context(), routeCreateRepository.at("ACME"), func() {}, nil), "a body that does not encode")
}

func TestStringsAHostChoseAreBounded(t *testing.T) {
	long := strings.Repeat("a", maxHostString+10)
	assert.Equal(t, maxHostString+len("…"), len(bounded(long)))
	assert.Equal(t, "x", withoutComment("x"))
	assert.Equal(t, "ssh-ed25519 AAAA", withoutComment("ssh-ed25519 AAAA tau@host"))

	d := &driver{host: "bb.example.com"}
	_, err := d.keyOf("", "pay")
	assert.Error(t, err)
	_, err = d.keyOf("ACME", "..")
	assert.Error(t, err)
	_, err = d.keyOf("A B", "pay")
	assert.Error(t, err, "a project no key can carry")
}
