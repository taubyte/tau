package auth

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
)

// identity.go is where a module outside this package decides who may use the
// cloud. Auth itself has no opinion: a forge has already said who holds a
// credential, and what that person may do here is the Identity's answer.

// Caller is someone a forge vouched for.
type Caller struct {
	// Forge is the host that answered for the credential, e.g. "github.com".
	Forge string
	// Subject is the forge's stable id for the account.
	Subject string
	// Login is the account's name on the forge.
	Login string
}

// Admission is what an Identity concluded about a Caller.
type Admission struct {
	Admitted bool
	// Reason says why a caller was not admitted, in words it can act on.
	Reason string
	// Accounts are what the caller resolved to. Auth carries them and does
	// not read them.
	Accounts []string
}

// Identity decides whether a caller may use this cloud's API.
type Identity interface {
	Admit(ctx context.Context, c Caller) (Admission, error)
}

// AdmitAll is the identity a node composed with none has: whoever the forge
// vouched for may use the API, and what they may touch is still decided per
// repository by the forge.
type AdmitAll struct{}

func (AdmitAll) Admit(context.Context, Caller) (Admission, error) {
	return Admission{Admitted: true}, nil
}

// WithIdentity sets who decides admission. Without it, AdmitAll.
func WithIdentity(i Identity) Option {
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
	a, err := h.s.identity.Admit(ctx, c)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	return connect.NewResponse(&authv2.Admission{Admitted: a.Admitted, Reason: a.Reason, Accounts: a.Accounts}), nil
}
