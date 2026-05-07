package accounts

import (
	"errors"
	"fmt"

	"github.com/fxamacker/cbor/v2"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
)

// Member-facing management routes (/members, /users). Plans and Accounts are
// operator-only and have no HTTP surface.

func (c *Client) sendMgmt(route string, body map[string]any, outField string, out any) error {
	var resp map[string]any
	if err := c.do("POST", route, body, &resp, true); err != nil {
		return err
	}
	if out == nil {
		if ok, _ := resp["ok"].(bool); !ok {
			return errors.New("server did not confirm")
		}
		return nil
	}
	v, ok := resp[outField]
	if !ok {
		return fmt.Errorf("response missing %q", outField)
	}
	raw, err := cbor.Marshal(v)
	if err != nil {
		return fmt.Errorf("re-encode %s: %w", outField, err)
	}
	if err := cbor.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode %s: %w", outField, err)
	}
	return nil
}

// --- Members ------------------------------------------------------

func (c *Client) InviteMember(accountID, email string, role accountsIface.Role) (*accountsIface.Member, error) {
	if accountID == "" || email == "" || role == "" {
		return nil, errors.New("InviteMember: account_id, email, role required")
	}
	var out accountsIface.Member
	if err := c.sendMgmt("/members", map[string]any{
		"action":        "invite",
		"account_id":    accountID,
		"primary_email": email,
		"role":          string(role),
	}, "member", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListMembers(accountID string) ([]string, error) {
	if accountID == "" {
		return nil, errors.New("ListMembers: account_id required")
	}
	var out []string
	if err := c.sendMgmt("/members", map[string]any{
		"action":     "list",
		"account_id": accountID,
	}, "ids", &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) GetMember(accountID, memberID string) (*accountsIface.Member, error) {
	if accountID == "" || memberID == "" {
		return nil, errors.New("GetMember: account_id, member_id required")
	}
	var out accountsIface.Member
	if err := c.sendMgmt("/members", map[string]any{
		"action":     "get",
		"account_id": accountID,
		"id":         memberID,
	}, "member", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// --- Users (linked git accounts) ----------------------------------

func (c *Client) AddUser(accountID, provider, externalID, displayName string) (*accountsIface.User, error) {
	if accountID == "" || provider == "" || externalID == "" {
		return nil, errors.New("AddUser: account_id, provider, external_id required")
	}
	var out accountsIface.User
	if err := c.sendMgmt("/users", map[string]any{
		"action":       "add",
		"account_id":   accountID,
		"provider":     provider,
		"external_id":  externalID,
		"display_name": displayName,
	}, "user", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListUsers(accountID string) ([]string, error) {
	if accountID == "" {
		return nil, errors.New("ListUsers: account_id required")
	}
	var out []string
	if err := c.sendMgmt("/users", map[string]any{
		"action":     "list",
		"account_id": accountID,
	}, "ids", &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) RemoveUser(accountID, userID string) error {
	if accountID == "" || userID == "" {
		return errors.New("RemoveUser: account_id, user_id required")
	}
	return c.sendMgmt("/users", map[string]any{
		"action":     "remove",
		"account_id": accountID,
		"id":         userID,
	}, "", nil)
}

func (c *Client) GrantPlan(accountID, userID, planID string) error {
	if accountID == "" || userID == "" || planID == "" {
		return errors.New("GrantPlan: account_id, user_id, plan_id required")
	}
	return c.sendMgmt("/users", map[string]any{
		"action":     "grant",
		"account_id": accountID,
		"id":         userID,
		"plan_id":    planID,
	}, "", nil)
}
