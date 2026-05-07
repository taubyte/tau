package accounts

import (
	"context"
	"fmt"

	"github.com/fxamacker/cbor/v2"
	"github.com/ipfs/go-log/v2"
	peerCore "github.com/libp2p/go-libp2p/core/peer"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
	peer "github.com/taubyte/tau/p2p/peer"
	streamClient "github.com/taubyte/tau/p2p/streams/client"
	"github.com/taubyte/tau/p2p/streams/command"
	protocolCommon "github.com/taubyte/tau/services/common"
)

var logger = log.Logger("tau.accounts.client")

// Client is the P2P client for the Accounts service. Wraps a stream client
// against the protocol registered by services/accounts; per-entity wire
// methods live in accounts.go / members.go / users.go / plans.go /
// tokens.go / login.go.
type Client struct {
	client *streamClient.Client
	node   peer.Node
	peers  []peerCore.ID
}

var _ accountsIface.Client = (*Client)(nil)

// New constructs a P2P accounts client over the given node.
func New(ctx context.Context, node peer.Node) (accountsIface.Client, error) {
	var (
		c   Client
		err error
	)
	c.client, err = streamClient.New(node, protocolCommon.AccountsProtocol)
	if err != nil {
		logger.Error("accounts client creation failed:", err.Error())
		return nil, err
	}
	c.node = node
	return &c, nil
}

// Close releases the underlying P2P stream client.
func (c *Client) Close() {
	c.client.Close()
}

// Peers narrows subsequent calls to a specific peer subset.
func (c *Client) Peers(peers ...peerCore.ID) accountsIface.Client {
	cp := *c
	cp.peers = peers
	return &cp
}

// --- Integration surface (verify + plan-resolve) ----------------

// Verify asks an accounts service node whether a (provider, external_id) git
// account is linked to ≥1 Account.
func (c *Client) Verify(ctx context.Context, provider, externalID string) (*accountsIface.VerifyResponse, error) {
	resp, err := c.client.Send(verbVerify, command.Body{
		"provider":    provider,
		"external_id": externalID,
	}, c.peers...)
	if err != nil {
		return nil, fmt.Errorf("accounts.Verify: %w", err)
	}
	return decodeVerifyResponse(resp)
}

// ResolvePlan asks an accounts service node whether (account_slug,
// plan_slug) names an active Plan the (provider, external_id) git user
// has a grant on.
func (c *Client) ResolvePlan(ctx context.Context, accountSlug, planSlug, provider, externalID string) (*accountsIface.ResolveResponse, error) {
	resp, err := c.client.Send(verbResolve, command.Body{
		"account_slug": accountSlug,
		"plan_slug":    planSlug,
		"provider":     provider,
		"external_id":  externalID,
	}, c.peers...)
	if err != nil {
		return nil, fmt.Errorf("accounts.ResolvePlan: %w", err)
	}
	return decodeResolveResponse(resp)
}

// --- Management surface --------------------------------------------

func (c *Client) Accounts() accountsIface.Accounts { return &accountsImpl{c: c} }
func (c *Client) Members(accountID string) accountsIface.Members {
	return &membersImpl{c: c, accountID: accountID}
}
func (c *Client) Users(accountID string) accountsIface.Users {
	return &usersImpl{c: c, accountID: accountID}
}
func (c *Client) Plans(accountID string) accountsIface.Plans {
	return &plansImpl{c: c, accountID: accountID}
}
func (c *Client) Login() accountsIface.Login { return &loginImpl{c: c} }

// --- Verify / Resolve verb constants and decoding ----------------

const (
	verbVerify  = "verify"
	verbResolve = "resolve"
)

func decodeVerifyResponse(resp map[string]any) (*accountsIface.VerifyResponse, error) {
	out := &accountsIface.VerifyResponse{
		Linked: tryBool(resp, "linked"),
	}
	if v, ok := resp["accounts"]; ok {
		raw, err := cbor.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("accounts.Verify: re-encode accounts: %w", err)
		}
		if err := cbor.Unmarshal(raw, &out.Accounts); err != nil {
			return nil, fmt.Errorf("accounts.Verify: decode accounts: %w", err)
		}
	}
	return out, nil
}

func decodeResolveResponse(resp map[string]any) (*accountsIface.ResolveResponse, error) {
	out := &accountsIface.ResolveResponse{
		Valid:  tryBool(resp, "valid"),
		Reason: tryString(resp, "reason"),
	}
	if v, ok := resp["plan"]; ok {
		raw, err := cbor.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("accounts.ResolvePlan: re-encode plan: %w", err)
		}
		var b accountsIface.Plan
		if err := cbor.Unmarshal(raw, &b); err != nil {
			return nil, fmt.Errorf("accounts.ResolvePlan: decode plan: %w", err)
		}
		out.Plan = &b
	}
	return out, nil
}

func tryBool(m map[string]interface{}, key string) bool {
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

func tryString(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
