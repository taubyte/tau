package cm

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	kvdbmock "github.com/taubyte/tau/pkg/kvdb/mock"
	cmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/cm/v2"
)

const (
	projectID  = "QmNTj7BY3YNySGaUrfuWjkomCME8fTcynvgHiNdX1hyLME"
	otherID    = "QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB"
	customZone = "shop.test"
)

// records is a DNS zone a test writes TXT records into.
type records struct {
	mu sync.Mutex
	m  map[string][]string
}

func (r *records) set(name string, values ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[name] = values
}

func (r *records) LookupTXT(_ context.Context, name string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.m[name]
	if !ok {
		return nil, errors.New("no such host")
	}
	return v, nil
}

func (f *fleet) domains(t *testing.T) *Domains {
	t.Helper()
	d, err := NewDomains(newHost(t, f.mn), f.members, func() *Service { return nil }, f.txt)
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })
	f.connect(t)
	return d
}

func (f *fleet) publish(t *testing.T, c *cmv2.DomainClaim) {
	t.Helper()
	require.Equal(t, "txt", c.GetType())
	f.txt.set(c.GetEntry(), c.GetToken())
}

func TestAClaimInTheTXTRecordProvesTheDomain(t *testing.T) {
	f := newFleet(t, 2, false)
	d := f.domains(t)

	require.ErrorIs(t, d.Prove(t.Context(), projectID, customZone), ErrUnproven, "no node has a key yet")

	c, err := d.Claim(t.Context(), projectID, "Shop.Test.")
	require.NoError(t, err)
	require.Equal(t, projectID[:8]+"."+customZone, c.GetEntry())
	require.ErrorIs(t, d.Prove(t.Context(), projectID, customZone), ErrUnproven, "the record is not set")

	f.publish(t, c)
	require.NoError(t, d.Prove(t.Context(), projectID, customZone))
	require.ErrorIs(t, d.Prove(t.Context(), otherID, customZone), ErrUnproven, "another project's record is at another name")
	f.txt.set(otherID[:8]+"."+customZone, c.GetToken())
	require.ErrorIs(t, d.Prove(t.Context(), otherID, customZone), ErrUnproven, "a claim is for one project")
	require.ErrorIs(t, d.Prove(t.Context(), "not a cid", customZone), ErrUnproven)

	// EVERY NODE'S KEY VERIFIES: a claim signed by the second node proves too.
	second, err := f.nodes[1].claim(t.Context(), "b."+customZone, projectID)
	require.NoError(t, err)
	f.publish(t, second)
	require.NoError(t, d.Prove(t.Context(), projectID, "b."+customZone))
	keys, err := f.nodes[0].keys(t.Context())
	require.NoError(t, err)
	require.Len(t, keys, 2)

	// A NODE REOPENED SIGNS WITH THE KEY IT KEPT.
	again, err := New(f.nodes[0].host, f.db, f.cfg, WithVault(f.vault))
	require.NoError(t, err)
	_, public, err := again.signingKey(t.Context())
	require.NoError(t, err)
	_, before, err := f.nodes[0].signingKey(t.Context())
	require.NoError(t, err)
	require.Equal(t, before, public)

	f.txt.set(c.GetEntry(), "garbage")
	require.ErrorIs(t, d.Prove(t.Context(), projectID, customZone), ErrUnproven)
}

func TestWhatAClaimRefuses(t *testing.T) {
	f := newFleet(t, 1, false)
	d := f.domains(t)
	_, err := d.Claim(t.Context(), "not a cid", customZone)
	require.ErrorIs(t, err, ErrBadClaim)
	_, err = d.Claim(t.Context(), projectID, "")
	require.ErrorIs(t, err, ErrBadClaim)

	_, err = NewDomains(nil, nil, nil, nil)
	require.ErrorIs(t, err, ErrNoHost)
	h := newHost(t, f.mn)
	none, err := NewDomains(h, func() []peer.ID { return nil }, func() *Service { return nil }, nil)
	require.NoError(t, err)
	_, err = none.Claim(t.Context(), projectID, customZone)
	require.ErrorIs(t, err, ErrNoMembers)
	require.ErrorIs(t, none.Prove(t.Context(), projectID, customZone), ErrNoMembers)

	gone, err := NewDomains(h, func() []peer.ID { return []peer.ID{"nobody"} }, func() *Service { return nil }, nil)
	require.NoError(t, err)
	_, err = gone.Claim(t.Context(), projectID, customZone)
	require.Error(t, err)
	require.ErrorIs(t, gone.Prove(t.Context(), projectID, customZone), ErrUnproven)

	db, err := kvdbmock.New().New(nil, "d", 0)
	require.NoError(t, err)
	keyless, err := New(h, db, f.cfg)
	require.NoError(t, err)
	_, err = keyless.claim(t.Context(), customZone, projectID)
	require.ErrorIs(t, err, ErrNoVault)
	broken, err := New(h, db, f.cfg, WithVault(&vault{data: map[string][]byte{}, fail: errors.New("down")}))
	require.NoError(t, err)
	_, err = broken.claim(t.Context(), customZone, projectID)
	require.ErrorContains(t, err, "down")

	// ONE PUBLISHED, ITS PRIVATE HALF LOST: refused rather than re-made.
	require.NoError(t, db.Put(t.Context(), domainKeyPrefix+h.ID().String(), []byte("pub")))
	_, err = broken.claim(t.Context(), customZone, projectID)
	require.ErrorContains(t, err, "reading the domain key")

	local, err := NewDomains(h, func() []peer.ID { return nil }, func() *Service { return f.nodes[0] }, f.txt)
	require.NoError(t, err)
	_, err = local.Claim(t.Context(), projectID, customZone)
	require.NoError(t, err, "a cm node answers its own")
}

func TestACustomDomainIsCertifiedOnlyOnceProven(t *testing.T) {
	f := newFleet(t, 1, false)
	d := f.domains(t)
	name := "www." + customZone

	c := f.client(t)
	_, err := c.ForProject(t.Context(), projectID, customZone, name)
	require.ErrorIs(t, err, ErrUnproven, "a client that proves nothing orders nothing outside the zone")

	c = f.client(t, WithProver(d))
	answer := c.Challenge
	f.http01.Store(&answer)

	_, err = c.ForProject(t.Context(), projectID, customZone, "www.elsewhere.test")
	require.ErrorIs(t, err, ErrNotZone)
	_, err = c.ForProject(t.Context(), projectID, customZone, name)
	require.ErrorIs(t, err, ErrUnproven)
	require.Zero(t, f.kept(t, name), "nothing is ordered for an unproven domain")

	claim, err := d.Claim(t.Context(), projectID, customZone)
	require.NoError(t, err)
	f.publish(t, claim)
	cert, err := c.ForProject(t.Context(), projectID, customZone, name)
	require.NoError(t, err)
	f.verify(t, cert, name)
	require.Equal(t, 1, f.kept(t, name))

	// A name in the cloud's own zone is the zone's, whatever is passed.
	inZone, err := c.ForProject(t.Context(), projectID, customZone, "a."+zone)
	require.NoError(t, err)
	f.verify(t, inZone, "a."+zone)

	// THE STORE PROVES TOO: a chain for a customZone name keeps only with a proof.
	s := f.nodes[0]
	rec := &cmv2.Issued{Name: name, Chain: []byte("x"), Key: &cmv2.SecretRef{Project: VaultProject, Environment: VaultEnvironment, Name: "k"}}
	require.ErrorIs(t, s.keep(t.Context(), rec), ErrNotZone)
	rec.Zone, rec.Project = customZone, otherID
	require.ErrorIs(t, s.keep(t.Context(), rec), ErrUnproven)
}
