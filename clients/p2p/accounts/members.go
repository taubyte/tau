package accounts

import (
	"context"
	"fmt"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/p2p/streams/command"
)

type membersImpl struct {
	c         *Client
	accountID string
}

func (i *membersImpl) Invite(ctx context.Context, in accountsIface.InviteMemberInput) (*accountsIface.Member, error) {
	resp, err := i.c.client.Send(verbMember, command.Body{
		"action":        "invite",
		"account_id":    i.accountID,
		"primary_email": in.PrimaryEmail,
		"role":          string(in.Role),
	}, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("members.Invite: %w", err)
	}
	var m accountsIface.Member
	if err := readField(resp, "member", &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (i *membersImpl) Get(ctx context.Context, memberID string) (*accountsIface.Member, error) {
	resp, err := i.c.client.Send(verbMember, command.Body{
		"action": "get", "account_id": i.accountID, "id": memberID,
	}, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("members.Get: %w", err)
	}
	var m accountsIface.Member
	if err := readField(resp, "member", &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (i *membersImpl) List(ctx context.Context) ([]string, error) {
	resp, err := i.c.client.Send(verbMember, command.Body{
		"action": "list", "account_id": i.accountID,
	}, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("members.List: %w", err)
	}
	var ids []string
	if err := readField(resp, "ids", &ids); err != nil {
		return nil, err
	}
	return ids, nil
}

func (i *membersImpl) Update(ctx context.Context, memberID string, in accountsIface.UpdateMemberInput) (*accountsIface.Member, error) {
	body := command.Body{
		"action":     "update",
		"account_id": i.accountID,
		"id":         memberID,
	}
	if in.Role != nil {
		body["role"] = string(*in.Role)
	}
	resp, err := i.c.client.Send(verbMember, body, i.c.peers...)
	if err != nil {
		return nil, fmt.Errorf("members.Update: %w", err)
	}
	var m accountsIface.Member
	if err := readField(resp, "member", &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (i *membersImpl) Remove(ctx context.Context, memberID string) error {
	resp, err := i.c.client.Send(verbMember, command.Body{
		"action": "remove", "account_id": i.accountID, "id": memberID,
	}, i.c.peers...)
	if err != nil {
		return fmt.Errorf("members.Remove: %w", err)
	}
	return expectOK(resp, "members.Remove")
}
