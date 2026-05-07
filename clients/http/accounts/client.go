// Package accounts is the HTTP client for the tau accounts service.
package accounts

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
)

// Client talks to the accounts service over HTTP.
type Client struct {
	url   string
	hc    *http.Client
	ctx   context.Context
	token string // Member-session bearer; set via WithSession
}

// Option configures the client.
type Option func(*Client) error

// New constructs a Client. Pass URL, then optional WithSession / WithUnsecure.
func New(ctx context.Context, opts ...Option) (*Client, error) {
	c := &Client{
		ctx: ctx,
		hc: &http.Client{
			Transport: &http.Transport{TLSClientConfig: &tls.Config{}},
		},
	}
	for _, opt := range opts {
		if err := opt(c); err != nil {
			return nil, err
		}
	}
	if c.url == "" {
		return nil, errors.New("accounts http: URL required (use WithURL)")
	}
	return c, nil
}

// WithURL sets the accounts-service base URL (e.g. https://accounts.tau.<network>).
func WithURL(u string) Option {
	return func(c *Client) error {
		c.url = u
		return nil
	}
}

// WithSession attaches a Member-session bearer for authenticated calls.
func WithSession(token string) Option {
	return func(c *Client) error {
		c.token = token
		return nil
	}
}

// WithUnsecure disables TLS verification. Test-only.
func WithUnsecure() Option {
	return func(c *Client) error {
		c.hc.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
		return nil
	}
}

// MeResponse mirrors services/accounts/http_endpoints.meResponse.
type MeResponse struct {
	Member   *accountsIface.Member                `json:"member,omitempty"`
	Accounts []accountsIface.VerifyAccountSummary `json:"accounts,omitempty"`
	Session  *accountsIface.Session               `json:"session,omitempty"`
}

// LoginStart kicks off a managed login. Returns the challenge response
// (MagicLinkSent or WebAuthnChallenge or Candidates).
func (c *Client) LoginStart(email, accountSlug string) (*accountsIface.ManagedLoginChallenge, error) {
	body := map[string]string{}
	if email != "" {
		body["email"] = email
	}
	if accountSlug != "" {
		body["account_slug"] = accountSlug
	}
	var out accountsIface.ManagedLoginChallenge
	if err := c.do("POST", "/login/start", body, &out, false); err != nil {
		return nil, err
	}
	return &out, nil
}

// FinishMagic completes a magic-link login. Returns the issued session.
func (c *Client) FinishMagic(code string) (*accountsIface.Session, error) {
	var sess accountsIface.Session
	if err := c.do("POST", "/login/finish/magic", map[string]string{"code": code}, &sess, false); err != nil {
		return nil, err
	}
	return &sess, nil
}

// Me returns the Member + Accounts associated with the current session.
// Requires WithSession on construction.
func (c *Client) Me() (*MeResponse, error) {
	if c.token == "" {
		return nil, errors.New("accounts http: Me() requires a session token (use WithSession)")
	}
	var out MeResponse
	if err := c.do("GET", "/me", nil, &out, true); err != nil {
		return nil, err
	}
	return &out, nil
}

// Logout revokes the current session bearer. Idempotent on the server.
func (c *Client) Logout() error {
	if c.token == "" {
		return errors.New("accounts http: Logout() requires a session token")
	}
	return c.do("POST", "/logout", nil, nil, true)
}

// do performs a JSON HTTP round-trip. When auth=true the session bearer is
// attached; otherwise the request is unauthenticated (login flow).
func (c *Client) do(method, path string, in any, out any, auth bool) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("accounts http: marshal %s body: %w", path, err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(c.ctx, method, c.url+path, body)
	if err != nil {
		return fmt.Errorf("accounts http: build %s %s: %w", method, path, err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("accounts http: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("accounts http: %s %s: status %s: %s", method, path, resp.Status, string(raw))
	}
	if out != nil {
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("accounts http: read %s body: %w", path, err)
		}
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("accounts http: decode %s body: %w", path, err)
		}
	}
	return nil
}
