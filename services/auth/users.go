package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"connectrpc.com/connect"

	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/services/auth/password"
	"github.com/taubyte/tau/services/secrets"
)

// AdminID is the built-in admin's user id. It is fixed, so every node names
// the admin alike without anything having been written.
const AdminID = "admin"

// Roles and capabilities as stored.
const (
	roleAdmin  = "admin"
	roleMember = "member"
	capLogin   = "login"
	capAPIKey  = "apikey"
)

type user struct {
	ID       string
	Name     string
	Email    string
	Role     string
	Disabled bool
	// Local is a user made by an admin rather than by a sign-in. Its
	// capabilities say what it may do; a provider's user may sign in through
	// its provider and hold API tokens.
	Local   bool
	Caps    []string
	Builtin bool
	Created int64
}

func (u user) can(c string) bool {
	switch {
	case u.Builtin:
		return c == capLogin
	case u.Local:
		return slices.Contains(u.Caps, c)
	default:
		return c == capAPIKey
	}
}

var errNoUsers = connect.NewError(connect.CodeFailedPrecondition, errors.New("auth: this node keeps no users"))

func newID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// validName is a local user's name: what they type to sign in.
var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

func (s *Service) admin() (user, bool) {
	a := s.cfg.Admin
	if a.Name == "" {
		return user{}, false
	}
	return user{ID: AdminID, Name: strings.ToLower(a.Name), Role: roleAdmin, Disabled: a.Disabled, Builtin: true}, true
}

// user loads one user. A deleted user is ErrNotFound.
func (s *Service) user(ctx context.Context, id string) (user, error) {
	if a, ok := s.admin(); ok && id == AdminID {
		return a, nil
	}
	if id == "" || strings.Contains(id, "/") {
		return user{}, ErrNotFound
	}
	st := s.store
	var p authv2.ProfileRow
	if err := st.get(ctx, userKey(id, fieldProfile), &p); err != nil {
		return user{}, err
	}
	gone, err := st.has(ctx, userKey(id, fieldDeleted))
	if err != nil {
		return user{}, err
	}
	if gone {
		return user{}, ErrNotFound
	}
	u := user{ID: id, Name: p.Name, Email: p.Email, Local: p.Local, Caps: p.Capabilities, Created: p.Created, Role: roleMember}
	var r authv2.RoleRow
	switch err := st.get(ctx, userKey(id, fieldRole), &r); {
	case err == nil:
		u.Role = r.Role
	case !errors.Is(err, ErrNotFound):
		return user{}, err
	}
	var d authv2.FlagRow
	switch err := st.get(ctx, userKey(id, fieldDisabled), &d); {
	case err == nil:
		u.Disabled = d.Set
	case !errors.Is(err, ErrNotFound):
		return user{}, err
	}
	return u, nil
}

// active is a user who may act: present and not disabled.
func (s *Service) active(ctx context.Context, id string) (user, error) {
	u, err := s.user(ctx, id)
	if err != nil {
		return user{}, err
	}
	if u.Disabled {
		return user{}, ErrNotFound
	}
	return u, nil
}

func (s *Service) live(ctx context.Context) func(string) bool {
	return func(id string) bool {
		_, err := s.active(ctx, id)
		return err == nil
	}
}

type newUser struct {
	Name  string
	Email string
	Role  string
	Local bool
	Caps  []string
	// Claim makes Name a sign-in name, which only a local user has.
	Claim bool
}

// create writes a new user. The name check is a guard for a clear error in
// the common case and nothing more: two nodes creating one name at once both
// succeed, and the name then resolves to the earlier (claims in store.go).
func (s *Service) create(ctx context.Context, n newUser) (user, error) {
	st := s.store
	n.Email = strings.ToLower(strings.TrimSpace(n.Email))
	if n.Claim {
		n.Name = strings.ToLower(n.Name)
		if !validName.MatchString(n.Name) {
			return user{}, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("auth: a name is lowercase letters, digits, '.', '_' and '-', starting with a letter or digit"))
		}
		if a, ok := s.admin(); ok && a.Name == n.Name {
			return user{}, connect.NewError(connect.CodeAlreadyExists, errors.New("auth: that name is taken"))
		}
		if _, _, err := st.winner(ctx, nameKey(n.Name), s.live(ctx)); err == nil {
			return user{}, connect.NewError(connect.CodeAlreadyExists, errors.New("auth: that name is taken"))
		} else if !errors.Is(err, ErrNotFound) {
			return user{}, err
		}
	}
	if strings.ContainsAny(n.Email, "/ ") {
		return user{}, connect.NewError(connect.CodeInvalidArgument, errors.New("auth: not an email address"))
	}
	id := newID()
	now := s.now().UnixNano()
	if err := st.put(ctx, userKey(id, fieldProfile), &authv2.ProfileRow{Name: n.Name, Email: n.Email, Local: n.Local, Capabilities: n.Caps, Created: now}); err != nil {
		return user{}, err
	}
	if n.Role == roleAdmin {
		if err := st.put(ctx, userKey(id, fieldRole), &authv2.RoleRow{Role: roleAdmin}); err != nil {
			return user{}, err
		}
	}
	if n.Claim {
		if err := st.put(ctx, nameKey(n.Name)+id, &authv2.ClaimRow{Created: now}); err != nil {
			return user{}, err
		}
	}
	if n.Email != "" {
		if err := st.put(ctx, emailKey(n.Email)+id, &authv2.ClaimRow{Created: now}); err != nil {
			return user{}, err
		}
	}
	return s.user(ctx, id)
}

// byName is the local user, or the admin, who signs in as name.
func (s *Service) byName(ctx context.Context, name string) (user, error) {
	name = strings.ToLower(name)
	if a, ok := s.admin(); ok && a.Name == name {
		return a, nil
	}
	if !validName.MatchString(name) {
		return user{}, ErrNotFound
	}
	id, _, err := s.store.winner(ctx, nameKey(name), s.live(ctx))
	if err != nil {
		return user{}, err
	}
	return s.user(ctx, id)
}

// link records that issuer's subject is id. The link is a claim of its own,
// so two users linking one identity are two rows and the identity resolves
// to the earlier; the copy under the user is for listing.
func (s *Service) link(ctx context.Context, id, issuer, subject, login string) error {
	c := &authv2.ClaimRow{Created: s.now().UnixNano(), Issuer: issuer, Subject: subject, Login: login}
	if err := s.store.put(ctx, linkKey(issuer, subject)+id, c); err != nil {
		return err
	}
	return s.store.put(ctx, userKey(id, fieldLinks)+"/"+digest(issuer, subject), c)
}

func (s *Service) linked(ctx context.Context, issuer, subject string) (user, error) {
	id, _, err := s.store.winner(ctx, linkKey(issuer, subject), s.live(ctx))
	if err != nil {
		return user{}, err
	}
	return s.user(ctx, id)
}

func (s *Service) links(ctx context.Context, id string) ([]*authv2.Link, error) {
	prefix := userKey(id, fieldLinks) + "/"
	ds, err := s.store.children(ctx, prefix)
	if err != nil {
		return nil, err
	}
	var out []*authv2.Link
	for _, d := range ds {
		var c authv2.ClaimRow
		if err := s.store.get(ctx, prefix+d, &c); err != nil {
			continue
		}
		out = append(out, &authv2.Link{Issuer: c.Issuer, Subject: c.Subject, Login: c.Login})
	}
	return out, nil
}

// remove deletes a user: a tombstone, so a concurrent write to one of its
// fields cannot bring it back, and then the claims it held.
func (s *Service) remove(ctx context.Context, u user) error {
	st := s.store
	if err := st.put(ctx, userKey(u.ID, fieldDeleted), &authv2.FlagRow{Set: true}); err != nil {
		return err
	}
	if u.Local && u.Name != "" {
		if err := st.del(ctx, nameKey(u.Name)+u.ID); err != nil {
			return err
		}
	}
	if u.Email != "" {
		if err := st.del(ctx, emailKey(u.Email)+u.ID); err != nil {
			return err
		}
	}
	prefix := userKey(u.ID, fieldLinks) + "/"
	ds, err := st.children(ctx, prefix)
	if err != nil {
		return err
	}
	for _, d := range ds {
		var c authv2.ClaimRow
		if err := st.get(ctx, prefix+d, &c); err == nil {
			if err := st.del(ctx, linkKey(c.Issuer, c.Subject)+u.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) list(ctx context.Context) ([]user, error) {
	var out []user
	if a, ok := s.admin(); ok {
		out = append(out, a)
	}
	ids, err := s.store.children(ctx, prefixUsers)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		u, err := s.user(ctx, id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, nil
}

func (s *Service) setPassword(ctx context.Context, id, pw string) error {
	if err := password.Check(pw); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	name, pepper, err := s.keys.Pepper(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnavailable, err)
	}
	release, err := s.hash(ctx)
	if err != nil {
		return err
	}
	defer release()
	h, err := password.New(pw, name, pepper, s.cfg.Params)
	if err != nil {
		return err
	}
	return s.store.put(ctx, userKey(id, fieldPassword), passwordRow(h))
}

// hash waits for one of the bounded Argon2 slots.
func (s *Service) hash(ctx context.Context) (func(), error) {
	select {
	case s.hashing <- struct{}{}:
		return func() { <-s.hashing }, nil
	case <-ctx.Done():
		return nil, connect.NewError(connect.CodeResourceExhausted, ctx.Err())
	}
}

func (s *Service) checkPassword(ctx context.Context, u user, pw string) error {
	var h password.Hash
	var pepper []byte
	if u.Builtin {
		phc, err := s.keys.vault.Get(ctx, AdminSecret)
		if errors.Is(err, secrets.ErrNotSet) {
			serviceLog.Warnf("admin login refused: the admin password has not been initialized in the secrets service (secret %q is not set)", AdminSecret)
			return connect.NewError(connect.CodeNotFound, fmt.Errorf("auth: the admin has no password in the secrets service yet: %w", err))
		}
		if err != nil {
			serviceLog.Errorf("admin login refused: reading secret %q from the secrets service: %s", AdminSecret, err)
			return connect.NewError(connect.CodeUnavailable, fmt.Errorf("auth: the admin cannot sign in until the secrets service answers: %w", err))
		}
		if h, err = password.Parse(string(phc)); err != nil {
			serviceLog.Errorf("admin login refused: secret %q in the secrets service is not a password hash; it is corrupted and must be reset", AdminSecret)
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("auth: the admin's password in the secrets service is not a hash: %w", err))
		}
	} else {
		var row authv2.PasswordRow
		if err := s.store.get(ctx, userKey(u.ID, fieldPassword), &row); err != nil {
			return err
		}
		h = passwordFrom(&row)
		if h.Pepper != "" {
			p, err := s.keys.PepperNamed(ctx, h.Pepper)
			if err != nil {
				return err
			}
			pepper = p
		}
	}
	release, err := s.hash(ctx)
	if err != nil {
		return err
	}
	defer release()
	return h.Verify(pw, pepper)
}

func (s *Service) toProto(ctx context.Context, u user) (*authv2.User, error) {
	p := &authv2.User{Id: u.ID, Name: u.Name, Email: u.Email, Disabled: u.Disabled, Builtin: u.Builtin, CreatedUnix: u.Created / 1e9}
	p.Role = authv2.Role_ROLE_MEMBER
	if u.Role == roleAdmin {
		p.Role = authv2.Role_ROLE_ADMIN
	}
	for _, c := range u.Caps {
		switch c {
		case capLogin:
			p.Capabilities = append(p.Capabilities, authv2.Capability_CAPABILITY_LOGIN)
		case capAPIKey:
			p.Capabilities = append(p.Capabilities, authv2.Capability_CAPABILITY_APIKEY)
		}
	}
	if !u.Builtin {
		links, err := s.links(ctx, u.ID)
		if err != nil {
			return nil, err
		}
		p.Links = links
	}
	return p, nil
}
