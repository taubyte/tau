package auth

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
)

// An Identity, outside this package, decides who may use the cloud; auth has no
// opinion.

type Caller struct {
	// Forge is the host that answered for the credential, e.g. "github.com".
	Forge string
	// Subject is the forge's stable id for the account.
	Subject string
	Login   string
	// User is the tau user the caller's own tau token verified as, or empty
	// when they sent none.
	User string
}

type Admission struct {
	Admitted bool
	// Reason says why a caller was not admitted, in words it can act on.
	Reason string
	// Accounts are what the caller resolved to. Auth carries them and does
	// not read them.
	Accounts []string
}

type Identity interface {
	Admit(ctx context.Context, c Caller) (Admission, error)
}

// AdmitAll admits whoever the forge vouched for; what they may touch is still
// decided per repository by the forge. It is the identity of an auth that keeps
// no users.
type AdmitAll struct{}

func (AdmitAll) Admit(context.Context, Caller) (Admission, error) {
	return Admission{Admitted: true}, nil
}

// Users is the identity of an auth that keeps users: a caller is admitted when
// they are a user of this cloud — their tau token says so, or the forge
// identity they hold is linked to one — or when sign-up is open.
type Users struct{ s *Service }

func (u *Users) Admit(ctx context.Context, c Caller) (Admission, error) {
	if c.User != "" {
		return Admission{Admitted: true}, nil
	}
	_, err := u.s.linked(ctx, c.Forge, c.Subject)
	switch {
	case err == nil:
		return Admission{Admitted: true}, nil
	case !errors.Is(err, ErrNotFound):
		return Admission{}, err
	case u.s.cfg.SignUp == SignUpOpen:
		return Admission{Admitted: true}, nil
	}
	return Admission{Reason: "not a user of this cloud: sign in to it first"}, nil
}

// withIdentity sets who decides admission. Without it, Users when auth keeps
// users and AdmitAll when it does not.
func withIdentity(i Identity) Option {
	return func(s *Service) error {
		if i == nil {
			return errors.New("auth: a nil identity")
		}
		s.identity = i
		return nil
	}
}

type identityHandler struct{ s *Service }

func (h *identityHandler) Admit(ctx context.Context, req *connect.Request[authv2.Caller]) (*connect.Response[authv2.Admission], error) {
	c := Caller{Forge: req.Msg.GetForge(), Subject: req.Msg.GetSubject(), Login: req.Msg.GetLogin()}
	if c.Forge == "" || c.Subject == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("auth: a caller needs a forge and a subject"))
	}
	// A TOKEN THAT DOES NOT VERIFY IS A REFUSAL WITH A REASON, not an error:
	// the caller can fix it by signing in again.
	if t := req.Msg.GetToken(); t != "" {
		p, err := h.s.verify(ctx, t)
		var ce *connect.Error
		switch {
		case errors.As(err, &ce) && ce.Code() == connect.CodeUnauthenticated:
			return connect.NewResponse(&authv2.Admission{Reason: "the tau token is not valid: sign in again"}), nil
		case err != nil:
			return nil, err
		}
		c.User = p.user.ID
	}
	a, err := h.s.identity.Admit(ctx, c)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	return connect.NewResponse(&authv2.Admission{Admitted: a.Admitted, Reason: a.Reason, Accounts: a.Accounts}), nil
}

func (h *identityHandler) Verify(ctx context.Context, req *connect.Request[authv2.VerifyRequest]) (*connect.Response[authv2.Principal], error) {
	p, err := h.s.verify(ctx, req.Msg.GetToken())
	if err != nil {
		return nil, err
	}
	u, err := h.s.toProto(ctx, p.user)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&authv2.Principal{User: u, TokenId: p.tokenID}), nil
}
