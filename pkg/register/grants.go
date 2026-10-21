package register

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// A project's roles are granted one key per grant and revoked one key per
// revocation, so two owners granting at once write different keys and a
// revocation can never be lost to a concurrent re-grant of the same id. The
// owner of the owning claim is an owner without a grant.

const (
	grantPrefix   = recordsPrefix + "grant" + sep
	grantedPrefix = recordsPrefix + "granted" + sep
	revokePrefix  = recordsPrefix + "revoke" + sep
)

// Role is what a tau user may do on a project. Each includes the ones before it.
type Role string

const (
	RoleNone          Role = ""
	RoleViewer        Role = "viewer"
	RoleBuilder       Role = "builder"
	RoleSecretManager Role = "secret-manager"
	RoleOwner         Role = "owner"
)

var roleRank = map[Role]int{RoleViewer: 1, RoleBuilder: 2, RoleSecretManager: 3, RoleOwner: 4}

// Includes reports whether r grants at least what want does.
func (r Role) Includes(want Role) bool {
	return r != RoleNone && roleRank[r] >= roleRank[want]
}

// ParseRole is a role by name; anything else is ErrNoRole.
func ParseRole(s string) (Role, error) {
	r := Role(s)
	if _, ok := roleRank[r]; !ok {
		return RoleNone, fmt.Errorf("%w: %q", ErrNoRole, s)
	}
	return r, nil
}

var (
	ErrNoRole    = errors.New("register: not a role")
	ErrNoGrantee = errors.New("register: a grant names no user")
	ErrNoGrant   = errors.New("register: no such grant")
)

// Grant is one role given to one user on one project.
type Grant struct {
	ID      string
	Project string
	Grantee string
	Role    Role
	By      string
	At      time.Time
}

type grantRow struct {
	Role string `json:"role"`
	At   int64  `json:"at"`
	By   string `json:"by"`
}

type revokeRow struct {
	At int64  `json:"at"`
	By string `json:"by"`
}

func grantKey(project, grantee, id string) string {
	return grantPrefix + project + sep + segment(grantee) + sep + id
}

func grantedKey(grantee, project, id string) string {
	return grantedPrefix + segment(grantee) + sep + project + sep + id
}

func revokeKey(project, id string) string { return revokePrefix + project + sep + id }

// GrantRole gives grantee role on a linked project, recorded as given by by.
func (r *Register) GrantRole(ctx context.Context, project, grantee string, role Role, by string) (Grant, error) {
	if err := checkName(project, ErrNoProject); err != nil {
		return Grant{}, err
	}
	grantee = strings.TrimSpace(grantee)
	if grantee == "" {
		return Grant{}, ErrNoGrantee
	}
	if _, err := ParseRole(string(role)); err != nil {
		return Grant{}, err
	}
	if _, found, err := r.owner(ctx, project); err != nil {
		return Grant{}, err
	} else if !found {
		return Grant{}, fmt.Errorf("%w: project %s", ErrNotLinked, project)
	}

	g := Grant{ID: rand.Text(), Project: project, Grantee: grantee, Role: role, By: by, At: r.now().UTC()}
	b, err := json.Marshal(grantRow{Role: string(role), At: g.At.UnixNano(), By: by})
	if err != nil {
		return Grant{}, err
	}
	for _, key := range []string{grantKey(project, grantee, g.ID), grantedKey(grantee, project, g.ID)} {
		if err := r.store.Put(ctx, key, b); err != nil {
			return Grant{}, fmt.Errorf("register: writing %s: %w", key, err)
		}
	}
	return g, nil
}

// Revoke withdraws one grant for good. An id the project never granted is
// ErrNoGrant.
func (r *Register) Revoke(ctx context.Context, project, id, by string) error {
	if err := checkName(project, ErrNoProject); err != nil {
		return err
	}
	if err := checkName(id, ErrNoGrant); err != nil {
		return err
	}
	grants, err := r.grants(ctx, project, grantPrefix+project+sep, false)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(grants, func(g Grant) bool { return g.ID == id }) {
		return fmt.Errorf("%w: %s on %s", ErrNoGrant, id, project)
	}
	b, err := json.Marshal(revokeRow{At: r.now().UnixNano(), By: by})
	if err != nil {
		return err
	}
	if err := r.store.Put(ctx, revokeKey(project, id), b); err != nil {
		return fmt.Errorf("register: writing %s: %w", revokeKey(project, id), err)
	}
	return nil
}

// Grants is every live grant on a project, oldest first, the id breaking ties.
func (r *Register) Grants(ctx context.Context, project string) ([]Grant, error) {
	if err := checkName(project, ErrNoProject); err != nil {
		return nil, err
	}
	return r.grants(ctx, project, grantPrefix+project+sep, true)
}

// grants reads the grant rows under prefix, which is project's grants or one
// grantee's on it.
func (r *Register) grants(ctx context.Context, project, prefix string, live bool) ([]Grant, error) {
	keys, err := r.store.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("register: listing grants on %s: %w", project, err)
	}
	var out []Grant
	for _, key := range keys {
		_, rest, ok := strings.Cut(key, grantPrefix+project+sep)
		if !ok {
			continue
		}
		seg, id, ok := strings.Cut(rest, sep)
		if !ok || id == "" || strings.Contains(id, sep) {
			continue
		}
		grantee, err := unsegment(seg)
		if err != nil {
			continue
		}
		g, ok, err := r.grant(ctx, project, grantee, id, live)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, g)
		}
	}
	slices.SortFunc(out, func(a, b Grant) int {
		if c := a.At.Compare(b.At); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (r *Register) grant(ctx context.Context, project, grantee, id string, live bool) (Grant, bool, error) {
	b, found, err := r.store.Get(ctx, grantKey(project, grantee, id))
	if err != nil {
		return Grant{}, false, fmt.Errorf("register: reading grant %s: %w", id, err)
	}
	var row grantRow
	if !found || json.Unmarshal(b, &row) != nil {
		return Grant{}, false, nil
	}
	role, err := ParseRole(row.Role)
	if err != nil {
		return Grant{}, false, nil
	}
	if live {
		_, revoked, err := r.store.Get(ctx, revokeKey(project, id))
		if err != nil {
			return Grant{}, false, fmt.Errorf("register: reading revocation %s: %w", id, err)
		}
		if revoked {
			return Grant{}, false, nil
		}
	}
	return Grant{ID: id, Project: project, Grantee: grantee, Role: role, By: row.By, At: time.Unix(0, row.At).UTC()}, true, nil
}

// RoleOf is user's role on a project: owner for the owning claim's claimant,
// otherwise the highest live grant, otherwise none. An unknown project is
// ErrNotLinked.
func (r *Register) RoleOf(ctx context.Context, project, user string) (Role, error) {
	if err := checkName(project, ErrNoProject); err != nil {
		return RoleNone, err
	}
	user = strings.TrimSpace(user)
	if user == "" {
		return RoleNone, ErrNoGrantee
	}
	owner, found, err := r.owner(ctx, project)
	if err != nil {
		return RoleNone, err
	}
	if !found {
		return RoleNone, fmt.Errorf("%w: project %s", ErrNotLinked, project)
	}
	if owner.Claimant == user {
		return RoleOwner, nil
	}
	grants, err := r.grants(ctx, project, grantPrefix+project+sep+segment(user)+sep, true)
	if err != nil {
		return RoleNone, err
	}
	best := RoleNone
	for _, g := range grants {
		if roleRank[g.Role] > roleRank[best] {
			best = g.Role
		}
	}
	return best, nil
}

// granted is every project user holds a live grant on.
func (r *Register) granted(ctx context.Context, user string) ([]string, error) {
	prefix := grantedPrefix + segment(user) + sep
	keys, err := r.store.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("register: listing %s's grants: %w", user, err)
	}
	var out []string
	for _, key := range keys {
		_, rest, found := strings.Cut(key, prefix)
		project, id, ok := strings.Cut(rest, sep)
		if !found || !ok || project == "" || id == "" || strings.Contains(id, sep) || slices.Contains(out, project) {
			continue
		}
		_, ok, err := r.grant(ctx, project, user, id, true)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, project)
		}
	}
	return out, nil
}
