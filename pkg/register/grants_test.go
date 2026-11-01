package register

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// on fails one operation on keys holding a substring.
type on struct {
	*MemStore
	op, sub string
}

func (s *on) Get(c context.Context, k string) ([]byte, bool, error) {
	if s.op == "get" && strings.Contains(k, s.sub) {
		return nil, false, errFailing
	}
	return s.MemStore.Get(c, k)
}

func (s *on) Put(c context.Context, k string, v []byte) error {
	if s.op == "put" && strings.Contains(k, s.sub) {
		return errFailing
	}
	return s.MemStore.Put(c, k, v)
}

func (s *on) List(c context.Context, p string) ([]string, error) {
	if s.op == "list" && strings.Contains(p, s.sub) {
		return nil, errFailing
	}
	return s.MemStore.List(c, p)
}

func ownedBy(t *testing.T, store Store) *Register {
	t.Helper()
	r := linked(t, store, nil)
	_, err := r.LinkProject(ctx, Link{Project: pid, Config: cfg, Code: []string{code}, Claimant: alice})
	require.NoError(t, err)
	return r
}

func TestTheClaimantOwnsAndGrantsGiveTheHighestRole(t *testing.T) {
	r := ownedBy(t, NewMemStore())

	role, err := r.RoleOf(ctx, pid, alice)
	require.NoError(t, err)
	require.Equal(t, RoleOwner, role)
	role, err = r.RoleOf(ctx, pid, "bob")
	require.NoError(t, err)
	require.Equal(t, RoleNone, role)

	viewer, err := r.GrantRole(ctx, pid, " bob ", RoleViewer, alice)
	require.NoError(t, err)
	require.Equal(t, "bob", viewer.Grantee)
	builder, err := r.GrantRole(ctx, pid, "bob", RoleBuilder, alice)
	require.NoError(t, err)
	require.NotEqual(t, viewer.ID, builder.ID, "two grants are two keys")

	role, err = r.RoleOf(ctx, pid, "bob")
	require.NoError(t, err)
	require.Equal(t, RoleBuilder, role)

	grants, err := r.Grants(ctx, pid)
	require.NoError(t, err)
	require.Equal(t, []string{viewer.ID, builder.ID}, []string{grants[0].ID, grants[1].ID}, "oldest first")
	require.Equal(t, alice, grants[0].By)

	ids, err := r.ProjectsOf(ctx, "bob")
	require.NoError(t, err)
	require.Len(t, ids, 1)
	require.Equal(t, pid, ids[0].ID)
}

func TestARevocationWins(t *testing.T) {
	store := NewMemStore()
	r := ownedBy(t, store)
	g, err := r.GrantRole(ctx, pid, "bob", RoleSecretManager, alice)
	require.NoError(t, err)

	require.NoError(t, r.Revoke(ctx, pid, g.ID, alice))
	role, err := r.RoleOf(ctx, pid, "bob")
	require.NoError(t, err)
	require.Equal(t, RoleNone, role)
	grants, err := r.Grants(ctx, pid)
	require.NoError(t, err)
	require.Empty(t, grants)
	mine, err := r.ProjectsOf(ctx, "bob")
	require.NoError(t, err)
	require.Empty(t, mine)

	// a concurrent writer re-putting the same grant row changes nothing
	b, found, err := store.Get(ctx, grantKey(pid, "bob", g.ID))
	require.NoError(t, err)
	require.True(t, found)
	require.NoError(t, store.Put(ctx, grantKey(pid, "bob", g.ID), b))
	role, err = r.RoleOf(ctx, pid, "bob")
	require.NoError(t, err)
	require.Equal(t, RoleNone, role)

	require.NoError(t, r.Revoke(ctx, pid, g.ID, alice), "revoking twice is still revoked")
	require.ErrorIs(t, r.Revoke(ctx, pid, "NOPE", alice), ErrNoGrant)
	require.ErrorIs(t, r.Revoke(ctx, pid, "", alice), ErrNoGrant)
	require.ErrorIs(t, r.Revoke(ctx, "a/b", g.ID, alice), ErrSeparator)
}

func TestGrantRefusals(t *testing.T) {
	r := ownedBy(t, NewMemStore())
	for _, c := range []struct {
		project, grantee string
		role             Role
		want             error
	}{
		{"", "bob", RoleViewer, ErrNoProject},
		{pid, " ", RoleViewer, ErrNoGrantee},
		{pid, "bob", Role("admin"), ErrNoRole},
		{"QmNobody", "bob", RoleViewer, ErrNotLinked},
	} {
		_, err := r.GrantRole(ctx, c.project, c.grantee, c.role, alice)
		require.ErrorIs(t, err, c.want)
	}
	_, err := r.RoleOf(ctx, pid, "")
	require.ErrorIs(t, err, ErrNoGrantee)
	_, err = r.RoleOf(ctx, "QmNobody", "bob")
	require.ErrorIs(t, err, ErrNotLinked)
	_, err = r.RoleOf(ctx, "a/b", "bob")
	require.ErrorIs(t, err, ErrSeparator)
	_, err = r.Grants(ctx, "")
	require.ErrorIs(t, err, ErrNoProject)

	for _, s := range []string{"viewer", "builder", "secret-manager", "owner"} {
		role, err := ParseRole(s)
		require.NoError(t, err)
		require.Equal(t, Role(s), role)
	}
}

func TestUnreadableGrantRowsGrantNothing(t *testing.T) {
	store := NewMemStore()
	r := ownedBy(t, store)
	for key, value := range map[string]string{
		grantKey(pid, "bob", "BAD"):                "{",
		grantKey(pid, "bob", "ROLE"):               `{"role":"admin"}`,
		grantPrefix + pid + sep + "!!" + sep + "X": `{"role":"owner"}`,
		grantPrefix + pid + sep + segment("bob"):   `{"role":"owner"}`,
		grantedPrefix + segment("bob") + sep + pid: `{"role":"owner"}`,
		grantedKey("bob", pid, "BAD"):              "{",
	} {
		require.NoError(t, store.Put(ctx, key, []byte(value)))
	}
	role, err := r.RoleOf(ctx, pid, "bob")
	require.NoError(t, err)
	require.Equal(t, RoleNone, role)
	grants, err := r.Grants(ctx, pid)
	require.NoError(t, err)
	require.Empty(t, grants)
	mine, err := r.ProjectsOf(ctx, "bob")
	require.NoError(t, err)
	require.Empty(t, mine)
}

func TestAStoreThatFailsUnderGrants(t *testing.T) {
	for _, c := range []struct {
		op, sub string
		call    func(r *Register) error
	}{
		{"put", grantPrefix, func(r *Register) error { _, err := r.GrantRole(ctx, pid, "bob", RoleViewer, alice); return err }},
		{"list", claimPrefix, func(r *Register) error { _, err := r.GrantRole(ctx, pid, "bob", RoleViewer, alice); return err }},
		{"list", grantPrefix, func(r *Register) error { _, err := r.Grants(ctx, pid); return err }},
		{"list", grantPrefix, func(r *Register) error { return r.Revoke(ctx, pid, "X", alice) }},
		{"list", grantPrefix, func(r *Register) error { _, err := r.RoleOf(ctx, pid, "bob"); return err }},
		{"list", claimPrefix, func(r *Register) error { _, err := r.RoleOf(ctx, pid, "bob"); return err }},
		{"get", grantPrefix, func(r *Register) error { _, err := r.Grants(ctx, pid); return err }},
		{"get", revokePrefix, func(r *Register) error { _, err := r.Grants(ctx, pid); return err }},
		{"put", revokePrefix, func(r *Register) error { return r.Revoke(ctx, pid, "G", alice) }},
		{"list", grantedPrefix, func(r *Register) error { _, err := r.ProjectsOf(ctx, "bob"); return err }},
		{"get", grantPrefix, func(r *Register) error { _, err := r.ProjectsOf(ctx, "bob"); return err }},
	} {
		store := &on{MemStore: NewMemStore()}
		r := ownedBy(t, store)
		require.NoError(t, store.Put(ctx, grantKey(pid, "bob", "G"), []byte(`{"role":"viewer"}`)))
		require.NoError(t, store.Put(ctx, grantedKey("bob", pid, "G"), []byte(`{"role":"viewer"}`)))
		store.op, store.sub = c.op, c.sub
		require.ErrorIs(t, c.call(r), errFailing, c.op+" "+c.sub)
	}
}

func TestAGrantedProjectThatIsGoneIsNotListed(t *testing.T) {
	store := NewMemStore()
	r := ownedBy(t, store)
	require.NoError(t, store.Put(ctx, grantKey("QmGone", "bob", "G"), []byte(`{"role":"viewer"}`)))
	require.NoError(t, store.Put(ctx, grantedKey("bob", "QmGone", "G"), []byte(`{"role":"viewer"}`)))
	_, err := r.GrantRole(ctx, pid, alice, RoleViewer, alice)
	require.NoError(t, err)

	mine, err := r.ProjectsOf(ctx, "bob")
	require.NoError(t, err)
	require.Empty(t, mine)
	mine, err = r.ProjectsOf(ctx, alice)
	require.NoError(t, err)
	require.Len(t, mine, 1, "owned and granted is listed once")

	store2 := &on{MemStore: NewMemStore()}
	r2 := ownedBy(t, store2)
	require.NoError(t, store2.Put(ctx, grantKey(pid, "bob", "G"), []byte(`{"role":"viewer"}`)))
	require.NoError(t, store2.Put(ctx, grantedKey("bob", pid, "G"), []byte(`{"role":"viewer"}`)))
	store2.op, store2.sub = "list", codePrefix
	_, err = r2.ProjectsOf(ctx, "bob")
	require.ErrorIs(t, err, errFailing)
}
