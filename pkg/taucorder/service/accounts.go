package service

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
	pb "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1"
)

func (as *accountsService) CreateAccount(ctx context.Context, req *connect.Request[pb.CreateAccountRequest]) (*connect.Response[pb.Account], error) {
	ni, err := as.getNode(req.Msg)
	if err != nil {
		return nil, err
	}
	if ni.accountsClient == nil {
		return nil, errors.New("accounts client not available on this node")
	}
	acc, err := ni.accountsClient.Accounts().Create(ctx, accountsIface.CreateAccountInput{
		Slug:     req.Msg.GetSlug(),
		Name:     req.Msg.GetName(),
		Kind:     accountsIface.AccountKind(req.Msg.GetKind()),
		AuthMode: accountsIface.AuthMode(req.Msg.GetAuthMode()),
	})
	if err != nil {
		return nil, fmt.Errorf("create account: %w", err)
	}
	return connect.NewResponse(accountToPB(acc)), nil
}

func (as *accountsService) AssignUser(ctx context.Context, req *connect.Request[pb.AssignUserRequest]) (*connect.Response[pb.User], error) {
	ni, err := as.getNode(req.Msg)
	if err != nil {
		return nil, err
	}
	if ni.accountsClient == nil {
		return nil, errors.New("accounts client not available on this node")
	}
	accID := req.Msg.GetAccountId()
	if accID == "" {
		return nil, errors.New("account_id required")
	}
	u, err := ni.accountsClient.Users(accID).Add(ctx, accountsIface.AddUserInput{
		Provider:    req.Msg.GetProvider(),
		ExternalID:  req.Msg.GetExternalId(),
		DisplayName: req.Msg.GetDisplayName(),
	})
	if err != nil {
		return nil, fmt.Errorf("assign user: %w", err)
	}
	return connect.NewResponse(userToPB(u)), nil
}

func (as *accountsService) ListAccounts(ctx context.Context, req *connect.Request[pb.Node], stream *connect.ServerStream[pb.Account]) error {
	ni, err := as.getNodeById(req.Msg.GetId())
	if err != nil {
		return err
	}
	if ni.accountsClient == nil {
		return errors.New("accounts client not available on this node")
	}
	ids, err := ni.accountsClient.Accounts().List(ctx)
	if err != nil {
		return fmt.Errorf("list accounts: %w", err)
	}
	for _, id := range ids {
		acc, err := ni.accountsClient.Accounts().Get(ctx, id)
		if err != nil {
			continue // skip stale index entries
		}
		if err := stream.Send(accountToPB(acc)); err != nil {
			return err
		}
	}
	return nil
}

func (as *accountsService) ListUsers(ctx context.Context, req *connect.Request[pb.ListUsersRequest], stream *connect.ServerStream[pb.User]) error {
	ni, err := as.getNode(req.Msg)
	if err != nil {
		return err
	}
	if ni.accountsClient == nil {
		return errors.New("accounts client not available on this node")
	}
	accID := req.Msg.GetAccountId()
	if accID == "" {
		return errors.New("account_id required")
	}
	ids, err := ni.accountsClient.Users(accID).List(ctx)
	if err != nil {
		return fmt.Errorf("list users: %w", err)
	}
	for _, id := range ids {
		u, err := ni.accountsClient.Users(accID).Get(ctx, id)
		if err != nil {
			continue
		}
		if err := stream.Send(userToPB(u)); err != nil {
			return err
		}
	}
	return nil
}

func accountToPB(a *accountsIface.Account) *pb.Account {
	return &pb.Account{
		Id:       a.ID,
		Slug:     a.Slug,
		Name:     a.Name,
		Kind:     string(a.Kind),
		Status:   string(a.Status),
		AuthMode: string(a.AuthMode),
	}
}

func userToPB(u *accountsIface.User) *pb.User {
	return &pb.User{
		Id:          u.ID,
		AccountId:   u.AccountID,
		Provider:    u.Provider,
		ExternalId:  u.ExternalID,
		DisplayName: u.DisplayName,
	}
}
