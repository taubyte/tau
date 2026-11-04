package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/register"
)

type fakeDomains struct {
	asked []string
	err   error
}

func (d *fakeDomains) Claim(_ context.Context, project, fqdn string) (DomainClaim, error) {
	d.asked = append(d.asked, project+" "+fqdn)
	return DomainClaim{Entry: "e." + fqdn, Type: "txt", Token: "signed"}, d.err
}

const domainPath = "/domain/shop.example.com/for/" + testProject

func TestTheOwnerGetsTheTXTRecordToSet(t *testing.T) {
	d := &fakeDomains{}
	h := newHarness(t, Config{}, WithDomains(d))

	w := h.do(t, authed(http.MethodPost, domainPath, testToken))
	require.Equal(t, http.StatusOK, w.Code)
	var got DomainClaim
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(t, DomainClaim{Entry: "e.shop.example.com", Type: "txt", Token: "signed"}, got)
	require.Equal(t, []string{testProject + " shop.example.com"}, d.asked)

	d.err = errors.New("cm is down")
	w = h.do(t, authed(http.MethodPost, domainPath, testToken))
	require.Equal(t, http.StatusBadGateway, w.Code)
}

func TestOnlyTheOwnerMayClaimADomain(t *testing.T) {
	d := &fakeDomains{}
	h := newHarness(t, Config{}, WithDomains(d))
	h.projects.role = func(string, string) (register.Role, error) { return register.RoleSecretManager, nil }
	w := h.do(t, authed(http.MethodPost, domainPath, testToken))
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Empty(t, d.asked)

	w = h.do(t, authed(http.MethodPost, domainPath, ""))
	require.Equal(t, http.StatusUnauthorized, w.Code)

	none := newHarness(t, Config{})
	w = none.do(t, authed(http.MethodPost, domainPath, testToken))
	require.Equal(t, http.StatusNotFound, w.Code)

	require.Error(t, WithDomains(nil)(&options{}))
}
