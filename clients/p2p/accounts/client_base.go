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

// Client is the P2P client for the Accounts service. Per-entity wire methods
// live in accounts.go / members.go / users.go / login.go.
type Client struct {
	client *streamClient.Client
	node   peer.Node
	peers  []peerCore.ID

	// eeSurface carries the ee-only methods — empty in the community build,
	// injected under -tags ee.
	eeSurface
}

var _ accountsIface.Client = (*Client)(nil)

// newBase wires the shared client. New (build-tagged) wraps it: the community
// build returns it as-is, the ee build injects the ee surface.
func newBase(ctx context.Context, node peer.Node) (*Client, error) {
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

func (c *Client) Close() {
	c.client.Close()
}

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

func (c *Client) LookupAccountsByEmail(ctx context.Context, email string) ([]string, error) {
	resp, err := c.client.Send(verbLookupAccountsByEmail, command.Body{
		"email": email,
	}, c.peers...)
	if err != nil {
		return nil, fmt.Errorf("accounts.LookupAccountsByEmail: %w", err)
	}
	var ids []string
	if v, ok := resp["account_ids"]; ok {
		raw, err := cbor.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("accounts.LookupAccountsByEmail: re-encode: %w", err)
		}
		if err := cbor.Unmarshal(raw, &ids); err != nil {
			return nil, fmt.Errorf("accounts.LookupAccountsByEmail: decode: %w", err)
		}
	}
	if ids == nil {
		ids = []string{}
	}
	return ids, nil
}

// sendLinkageResolve issues the community linkage check over the wire (verb
// "resolve"). Used by Validate in both builds' fallback path.
func (c *Client) sendLinkageResolve(accountSlug, provider, externalID string) (*accountsIface.ResolveResponse, error) {
	resp, err := c.client.Send(verbResolve, command.Body{
		"account_slug": accountSlug,
		"provider":     provider,
		"external_id":  externalID,
	}, c.peers...)
	if err != nil {
		return nil, fmt.Errorf("accounts.Validate: %w", err)
	}
	return decodeResolveResponse(resp)
}

func (c *Client) Accounts() accountsIface.Accounts { return &accountsImpl{c: c} }
func (c *Client) Members(accountID string) accountsIface.Members {
	return &membersImpl{c: c, accountID: accountID}
}
func (c *Client) Users(accountID string) accountsIface.Users {
	return &usersImpl{c: c, accountID: accountID}
}
func (c *Client) Login() accountsIface.Login { return &loginImpl{c: c} }

const (
	verbVerify                = "verify"
	verbResolve               = "resolve"
	verbLookupAccountsByEmail = "lookup_accounts_by_email"
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
	return &accountsIface.ResolveResponse{
		Valid:  tryBool(resp, "valid"),
		Reason: tryString(resp, "reason"),
	}, nil
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
