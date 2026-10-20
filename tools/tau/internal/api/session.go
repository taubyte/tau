package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
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

// Refreshing is Tokens kept in memory and renewed from a refresh token. Save
// is told about every change.
//
// A RENEWED TOKEN IS KEPT ONLY WHEN IT IS NEWER, by the order the cloud
// stamps in it — its session, then its generation (tau_gen: how many
// renewals and merges its seal descends from), then when it was issued —
// never by which response happened to arrive last. One of an older session
// (a stale answer from before a connect or a disconnect) is dropped.
//
// EVERY RENEWAL IS KEPT UNTIL THE NEXT REFRESH, newest or not, and sent with
// it: two requests renewing two forge hosts each hold one renewal, and the
// cloud merges them per host, so neither is lost. The newest is handed to
// Save the moment it arrives, so a process that exits before refreshing has
// already written it; Save is where the same rule is applied against what
// another process may have written since (see Supersedes).
type Refreshing struct {
	mu      sync.Mutex
	Token   string
	Expires int64
	Refresh string
	// renewals are the tokens handed back since the last refresh.
	renewals []string
	// Sessions renews; it is an anonymous client of the same cloud.
	Sessions authv2connect.SessionServiceClient
	Now      func() int64
	Save     func(token string, expires int64, refresh string)
}

// renewWithin is how close to its expiry a token is renewed before use.
const renewWithin = 60

// maxRenewals bounds the renewals kept for the next refresh.
const maxRenewals = 8

func (r *Refreshing) Access(ctx context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Refresh == "" || r.Expires == 0 || r.Now() < r.Expires-renewWithin {
		return r.Token, nil
	}
	resp, err := r.Sessions.Refresh(ctx, connect.NewRequest(&authv2.RefreshRequest{RefreshToken: r.Refresh, AccessTokens: append([]string{r.Token}, r.renewals...)}))
	if err != nil {
		if connect.CodeOf(err) == connect.CodeUnauthenticated {
			return "", errSignedOut
		}
		return "", err
	}
	s := resp.Msg.GetSession()
	r.Token, r.Expires, r.Refresh, r.renewals = s.GetAccessToken(), s.GetAccessExpiresUnix(), s.GetRefreshToken(), nil
	r.Save(r.Token, r.Expires, r.Refresh)
	return r.Token, nil
}

func (r *Refreshing) Renewed(token string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	got, ok := orderOf(token)
	if !ok {
		return
	}
	cur, curOK := orderOf(r.Token)
	if curOK && got.session != cur.session {
		return
	}
	if !slices.Contains(r.renewals, token) {
		r.renewals = append(r.renewals, token)
		if len(r.renewals) > maxRenewals {
			r.renewals = r.renewals[len(r.renewals)-maxRenewals:]
		}
	}
	if curOK && !got.after(cur) {
		return
	}
	r.Token = token
	if got.expires != 0 {
		r.Expires = got.expires
	}
	r.Save(r.Token, r.Expires, r.Refresh)
}

// Supersedes says whether token may replace than in a profile: than is not a
// tau token, or both are of one session and token is newer by its
// generation, then its issue time. A token of another session never
// replaces one: that is a sign-in this process did not make.
func Supersedes(token, than string) bool {
	got, ok := orderOf(token)
	if !ok {
		return false
	}
	cur, curOK := orderOf(than)
	if !curOK {
		return true
	}
	return got.session == cur.session && got.after(cur)
}

// order is what a tau access token says about where it stands in its
// session, read without verifying it: the cloud verifies, this only ranks
// tokens it handed out.
type order struct {
	session            string
	generation, issued int64
	expires            int64
}

func (o order) after(than order) bool {
	if o.generation != than.generation {
		return o.generation > than.generation
	}
	return o.issued > than.issued
}

func orderOf(token string) (order, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return order{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return order{}, false
	}
	var c struct {
		Session string `json:"sid"`
		Gen     int64  `json:"tau_gen"`
		Issued  int64  `json:"iat"`
		Expires int64  `json:"exp"`
	}
	if json.Unmarshal(payload, &c) != nil || c.Session == "" {
		return order{}, false
	}
	return order{session: c.Session, generation: c.Gen, issued: c.Issued, expires: c.Expires}, true
}

var errSignedOut = &Error{Status: http.StatusUnauthorized}
