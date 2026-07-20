//go:build dreaming

package service

import (
	"context"
	"net"
	"net/http"
	"slices"
	"testing"

	"connectrpc.com/connect"
	"github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/api"
	project "github.com/taubyte/tau/pkg/schema/project"
	pb "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1"
	pbconnect "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1/taucorderv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/services/accounts/dream"
)

// TestAccounts_Dreaming is the taucorder accounts e2e: taucorder dials a real
// accounts service in a dream universe, creates an Account and assigns (links) a
// git User over the Connect API, then confirms the linkage is the access grant
// by verifying/resolving through the universe's own accounts client.
func TestAccounts_Dreaming(t *testing.T) {
	ctx, ctxC := context.WithCancel(context.Background())
	defer ctxC()

	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	uname := t.Name()
	u, err := m.New(dream.UniverseConfig{Name: uname})
	assert.NilError(t, err)

	assert.NilError(t, api.BigBang(m))

	s, err := getMockService(ctx)
	assert.NilError(t, err)

	ns := &nodeService{Service: s}
	as := &accountsService{Service: s}
	s.addHandler(pbconnect.NewNodeServiceHandler(ns))

	assert.NilError(t, u.StartWithConfig(&dream.Config{Services: map[string]common.ServiceConfig{
		"accounts": {},
	}}))

	ni, err := ns.New(ctx, connect.NewRequest(&pb.Config{
		Source: &pb.Config_Universe{
			Universe: &pb.Dream{Universe: uname},
		},
	}))
	assert.NilError(t, err)
	defer ns.Free(ctx, connect.NewRequest(ni.Msg))

	listener, err := net.Listen("tcp", ":0")
	assert.NilError(t, err)
	defer listener.Close()

	path, handler := pbconnect.NewAccountsServiceHandler(as)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := &http.Server{Handler: h2c.NewHandler(mux, &http2.Server{})}
	go func() { _ = server.Serve(listener) }()
	defer server.Shutdown(ctx)

	c := pbconnect.NewAccountsServiceClient(http.DefaultClient, "http://"+listener.Addr().String())

	// CreateAccount
	acc, err := c.CreateAccount(ctx, connect.NewRequest(&pb.CreateAccountRequest{
		Node: ni.Msg, Slug: "acme", Name: "Acme Corp",
	}))
	assert.NilError(t, err)
	assert.Equal(t, acc.Msg.GetSlug(), "acme")
	accID := acc.Msg.GetId()
	assert.Assert(t, accID != "")

	// AssignUser (link a git identity → the community access grant)
	usr, err := c.AssignUser(ctx, connect.NewRequest(&pb.AssignUserRequest{
		Node: ni.Msg, AccountId: accID, Provider: "github", ExternalId: "42", DisplayName: "alice",
	}))
	assert.NilError(t, err)
	assert.Equal(t, usr.Msg.GetExternalId(), "42")
	assert.Equal(t, usr.Msg.GetAccountId(), accID)

	// ListAccounts streams our account back.
	astream, err := c.ListAccounts(ctx, connect.NewRequest(&pb.Node{Id: ni.Msg.GetId()}))
	assert.NilError(t, err)
	var slugs []string
	for astream.Receive() {
		slugs = append(slugs, astream.Msg().GetSlug())
	}
	assert.NilError(t, astream.Err())
	assert.Assert(t, slices.Contains(slugs, "acme"))

	// ListUsers streams our linked user back.
	ustream, err := c.ListUsers(ctx, connect.NewRequest(&pb.ListUsersRequest{Node: ni.Msg, AccountId: accID}))
	assert.NilError(t, err)
	var userIDs []string
	for ustream.Receive() {
		userIDs = append(userIDs, ustream.Msg().GetId())
	}
	assert.NilError(t, ustream.Err())
	assert.Assert(t, slices.Contains(userIDs, usr.Msg.GetId()))

	// Linkage is the access grant: Verify + Resolve succeed for the assigned
	// identity, straight against the universe's accounts client.
	cli := u.Accounts().Client()
	vr, err := cli.Verify(ctx, "github", "42")
	assert.NilError(t, err)
	assert.Equal(t, vr.Linked, true)

	rr, err := cli.Validate(ctx, "github", "42", project.CloudBinding{Account: "acme"})
	assert.NilError(t, err)
	assert.Equal(t, rr.Valid, true)

	// An unlinked identity resolves invalid.
	rr2, err := cli.Validate(ctx, "github", "999", project.CloudBinding{Account: "acme"})
	assert.NilError(t, err)
	assert.Equal(t, rr2.Valid, false)
}
