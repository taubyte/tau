package api

import (
	"context"
	"net/http"
	"sync"

	"connectrpc.com/connect"

	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
)

// session.go is the cloud's sign-in and user calls (auth's SessionService
// and UserService, carried by the frontend as ConnectRPC), and the one
// credential every call carries.

// RenewedTokenHeader is where the frontend hands back a renewed tau token,
// when a forge credential sealed in it was renewed during the request
// (services/build/frontend's RenewedTokenHeader).
const RenewedTokenHeader = "Tau-Renewed-Token"

// Tokens is where a client's tau token comes from and goes back to.
type Tokens interface {
	// Access is the token to send, renewed first when it is about to expire.
	Access(ctx context.Context) (string, error)
	// Renewed keeps a token the cloud handed back in place of the old one.
	Renewed(token string)
}

// Static is one token, never renewed.
type Static string

func (s Static) Access(context.Context) (string, error) { return string(s), nil }
func (Static) Renewed(string)                           {}

// bearer adds the tau token to every request and keeps a renewal.
type bearer struct {
	next   http.RoundTripper
	tokens Tokens
}

func transport(hc *http.Client) http.RoundTripper {
	if hc.Transport != nil {
		return hc.Transport
	}
	return http.DefaultTransport
}

func (b *bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Header.Get("Authorization") == "" {
		t, err := b.tokens.Access(r.Context())
		if err != nil {
			return nil, err
		}
		if t != "" {
			r = r.Clone(r.Context())
			r.Header.Set("Authorization", "Bearer "+t)
		}
	}
	resp, err := b.next.RoundTrip(r)
	if err == nil {
		if t := resp.Header.Get(RenewedTokenHeader); t != "" {
			b.tokens.Renewed(t)
		}
	}
	return resp, err
}

// Sessions is auth's sign-in surface, carrying the tau token when there is one.
func (c *Client) Sessions() authv2connect.SessionServiceClient {
	return authv2connect.NewSessionServiceClient(c.authed, c.base.String())
}

// Anonymous is the sign-in surface with no credential at all: a refresh is
// authorized by its refresh token alone.
func (c *Client) Anonymous() authv2connect.SessionServiceClient {
	return authv2connect.NewSessionServiceClient(c.http, c.base.String())
}

// Users is auth's user management.
func (c *Client) Users() authv2connect.UserServiceClient {
	return authv2connect.NewUserServiceClient(c.authed, c.base.String())
}

// OpenConnection is the forge credential the tau token carries for host, for
// talking to that forge directly.
func (c *Client) OpenConnection(ctx context.Context, host string) (string, error) {
	resp, err := c.Sessions().OpenConnection(ctx, connect.NewRequest(&authv2.OpenConnectionRequest{Host: host}))
	if err != nil {
		return "", err
	}
	if t := resp.Msg.GetRenewedToken(); t != "" {
		c.tokens.Renewed(t)
	}
	return resp.Msg.GetForge().GetAccessToken(), nil
}

// Refreshing is Tokens kept in memory and renewed from a refresh token. Save
// is told about every change.
type Refreshing struct {
	mu      sync.Mutex
	Token   string
	Expires int64
	Refresh string
	// Sessions renews; it is an anonymous client of the same cloud.
	Sessions authv2connect.SessionServiceClient
	Now      func() int64
	Save     func(token string, expires int64, refresh string)
}

// renewWithin is how close to its expiry a token is renewed before use.
const renewWithin = 60

func (r *Refreshing) Access(ctx context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Refresh == "" || r.Expires == 0 || r.Now() < r.Expires-renewWithin {
		return r.Token, nil
	}
	resp, err := r.Sessions.Refresh(ctx, connect.NewRequest(&authv2.RefreshRequest{RefreshToken: r.Refresh, AccessToken: r.Token}))
	if err != nil {
		if connect.CodeOf(err) == connect.CodeUnauthenticated {
			return "", errSignedOut
		}
		return "", err
	}
	s := resp.Msg.GetSession()
	r.Token, r.Expires, r.Refresh = s.GetAccessToken(), s.GetAccessExpiresUnix(), s.GetRefreshToken()
	r.Save(r.Token, r.Expires, r.Refresh)
	return r.Token, nil
}

func (r *Refreshing) Renewed(token string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Token = token
	r.Save(r.Token, r.Expires, r.Refresh)
}

var errSignedOut = &Error{Status: http.StatusUnauthorized}
