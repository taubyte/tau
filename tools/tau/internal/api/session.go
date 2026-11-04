package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/pkg/proto/codec"
	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
)

// session.go is the cloud's sign-in and user calls (auth's SessionService
// and UserService, carried by the build service as ConnectRPC), and the one
// credential every call carries.

// Tokens is where a client's tau token comes from.
type Tokens interface {
	// Access is the token to send, renewed first when it is about to expire.
	Access(ctx context.Context) (string, error)
}

// Static is one token, never renewed.
type Static string

func (s Static) Access(context.Context) (string, error) { return string(s), nil }

// bearer adds the tau token to every request.
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
	return b.next.RoundTrip(r)
}

// Sessions is auth's sign-in surface, carrying the tau token when there is one.
func (c *Client) Sessions() authv2connect.SessionServiceClient {
	return authv2connect.NewSessionServiceClient(c.authed, c.base.String(), codec.Option())
}

// Anonymous is the sign-in surface with no credential at all: a refresh is
// authorized by its refresh token alone.
func (c *Client) Anonymous() authv2connect.SessionServiceClient {
	return authv2connect.NewSessionServiceClient(c.http, c.base.String(), codec.Option())
}

func (c *Client) Users() authv2connect.UserServiceClient {
	return authv2connect.NewUserServiceClient(c.authed, c.base.String(), codec.Option())
}

// Refreshing is Tokens kept in memory and renewed from a refresh token. Save
// is told about every change; Save applies Supersedes against what another
// process may have written since.
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
	resp, err := r.Sessions.Refresh(ctx, connect.NewRequest(&authv2.RefreshRequest{RefreshToken: r.Refresh}))
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

// Supersedes says whether token may replace than in a profile: than is not a tau
// token, or both share a session and token was issued later. Another session's
// token never replaces one.
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
	session string
	issued  int64
}

func (o order) after(than order) bool {
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
	type cachedSession struct {
		Session string `json:"sid"`
		Issued  int64  `json:"iat"`
	}
	var c cachedSession
	if json.Unmarshal(payload, &c) != nil || c.Session == "" {
		return order{}, false
	}
	return order{session: c.Session, issued: c.Issued}, true
}

var errSignedOut = &Error{Status: http.StatusUnauthorized}
