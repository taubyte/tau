package auth

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"

	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
)

// invites.go is how someone becomes a user on an invite-only cloud.
//
// AN INVITE IS A BEARER CODE FOR A ROLE, keyed by the digest of the code, and
// it is not bound to an email: a forge's email is whatever its user set
// public, and binding an invite to one would trust it. Redeeming one writes a
// marker per redeeming user and the invite is refused once any marker exists.
// That guard is a read before a write, so two sign-ins on two nodes in the same
// instant can both redeem one invite; each becomes a user of the invite's
// role, which is what the admin handed out the code for. An admin who needs a
// code to admit exactly one person deletes it once it is used.

const inviteCodePrefix = "taui_"

func (s *Service) createInvite(ctx context.Context, by user, role string, ttl time.Duration, note string) (*authv2.Invite, error) {
	if ttl <= 0 {
		ttl = s.cfg.InviteTTL
	}
	if len(note) > 256 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("auth: a note is at most 256 bytes"))
	}
	code := inviteCodePrefix + randomToken()
	now := s.now()
	row := inviteRow{ID: digest(code)[:apiKeyIDLen], Role: role, Note: note, CreatedBy: by.ID, Created: now.UnixNano(), Expires: now.Add(ttl).UnixNano()}
	if err := s.store.put(ctx, prefixInvites+row.ID, row); err != nil {
		return nil, err
	}
	p := inviteProto(row)
	p.Code = code
	return p, nil
}

func inviteProto(r inviteRow) *authv2.Invite {
	p := &authv2.Invite{Id: r.ID, Role: authv2.Role_ROLE_MEMBER, ExpiresUnix: r.Expires / 1e9, Note: r.Note, CreatedBy: r.CreatedBy}
	if r.Role == roleAdmin {
		p.Role = authv2.Role_ROLE_ADMIN
	}
	return p
}

// invite is the invite code redeems, if it may still be redeemed.
func (s *Service) invite(ctx context.Context, code string) (inviteRow, error) {
	if code == "" {
		return inviteRow{}, ErrNotFound
	}
	id := digest(code)[:apiKeyIDLen]
	var row inviteRow
	if err := s.store.get(ctx, prefixInvites+id, &row); err != nil {
		return inviteRow{}, err
	}
	if s.now().UnixNano() >= row.Expires {
		return inviteRow{}, ErrNotFound
	}
	used, err := s.store.children(ctx, prefixInvites+id+"/redeemed/")
	if err != nil {
		return inviteRow{}, err
	}
	if len(used) > 0 {
		return inviteRow{}, ErrNotFound
	}
	return row, nil
}

func (s *Service) redeem(ctx context.Context, row inviteRow, userID string) error {
	return s.store.put(ctx, prefixInvites+row.ID+"/redeemed/"+userID, claim{Created: s.now().UnixNano()})
}

func (s *Service) listInvites(ctx context.Context) ([]*authv2.Invite, error) {
	ids, err := s.store.children(ctx, prefixInvites)
	if err != nil {
		return nil, err
	}
	var out []*authv2.Invite
	for _, id := range ids {
		var row inviteRow
		if err := s.store.get(ctx, prefixInvites+id, &row); err != nil {
			continue
		}
		out = append(out, inviteProto(row))
	}
	return out, nil
}

func (s *Service) deleteInvite(ctx context.Context, id string) error {
	return s.store.del(ctx, prefixInvites+id)
}
