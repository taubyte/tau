package accounts

import (
	"context"
	"errors"
	"fmt"
	"testing"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/p2p/keypair"
	"github.com/taubyte/tau/p2p/peer"
	tauConfig "github.com/taubyte/tau/pkg/config"
	mockkvdb "github.com/taubyte/tau/pkg/kvdb/mock"
)

// Targeted tests for paths not exercised by store_test.go. Goal is to push
// /services/accounts coverage to ~80% without redoing what's already there.

func TestNewAccountsConfig_Defaults(t *testing.T) {
	in := tauConfig.Accounts{
		SessionTTL: "12h",
		Email: tauConfig.AccountsEmail{
			SMTP: tauConfig.SMTP{Host: "smtp.example.com", Port: 587, From: "no-reply@example.com"},
		},
	}
	out := newAccountsConfig(in, "tau-cloud.io")
	if out.sessionTTL != "12h" {
		t.Fatalf("sessionTTL = %q, want %q", out.sessionTTL, "12h")
	}
	if out.emailSMTPHost != "smtp.example.com" || out.emailSMTPPort != 587 || out.emailSMTPFrom != "no-reply@example.com" {
		t.Fatalf("smtp not propagated: %+v", out)
	}

	// Empty `From` falls back to `noreply@<rootDomain>`.
	in.Email.SMTP.From = ""
	out = newAccountsConfig(in, "tau-cloud.io")
	if out.emailSMTPFrom != "noreply@tau-cloud.io" {
		t.Fatalf("default From = %q, want %q", out.emailSMTPFrom, "noreply@tau-cloud.io")
	}

	// Empty `From` + empty rootDomain → noreply@localhost (dev / unset FQDN).
	out = newAccountsConfig(in, "")
	if out.emailSMTPFrom != "noreply@localhost" {
		t.Fatalf("default From with no FQDN = %q, want %q", out.emailSMTPFrom, "noreply@localhost")
	}
}

func TestAccountStore_UpdateAllFields(t *testing.T) {
	srv := newTestService(t)
	store := newAccountStore(srv.db)
	ctx := context.Background()

	acc, err := store.Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Exercise every field path on Update.
	newName := "Acme Inc."
	newMode := accountsIface.AuthModeExternalOIDC
	newConfig := &accountsIface.AuthConfig{IssuerURL: "https://idp.example.com"}
	newPlan := "enterprise"
	newStatus := accountsIface.AccountStatusSuspended
	upd, err := store.Update(ctx, acc.ID, accountsIface.UpdateAccountInput{
		Name:         &newName,
		AuthMode:     &newMode,
		AuthConfig:   newConfig,
		PlanTemplate: &newPlan,
		Status:       &newStatus,
		Metadata:     map[string]string{"k1": "v1"},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if upd.Name != newName || upd.AuthMode != newMode {
		t.Fatalf("Update fields: %+v", upd)
	}
	if upd.AuthConfig == nil || upd.AuthConfig.IssuerURL != "https://idp.example.com" {
		t.Fatalf("Update AuthConfig: %+v", upd.AuthConfig)
	}
	if upd.PlanTemplate != newPlan || upd.Status != newStatus {
		t.Fatalf("Update plan/status: %+v", upd)
	}
	if upd.Metadata["k1"] != "v1" {
		t.Fatalf("Update metadata: %+v", upd.Metadata)
	}

	// Merge: existing metadata preserved when caller doesn't include keys.
	upd, _ = store.Update(ctx, acc.ID, accountsIface.UpdateAccountInput{Metadata: map[string]string{"k2": "v2"}})
	if upd.Metadata["k1"] != "v1" || upd.Metadata["k2"] != "v2" {
		t.Fatalf("Update metadata merge: %+v", upd.Metadata)
	}
}

func TestAccountStore_UpdateMissing(t *testing.T) {
	srv := newTestService(t)
	store := newAccountStore(srv.db)
	ctx := context.Background()
	if _, err := store.Update(ctx, "ghost", accountsIface.UpdateAccountInput{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update missing should return ErrNotFound, got %v", err)
	}
}

func TestAccountStore_DeleteMissing(t *testing.T) {
	srv := newTestService(t)
	store := newAccountStore(srv.db)
	ctx := context.Background()
	if err := store.Delete(ctx, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete missing should return ErrNotFound, got %v", err)
	}
}

func TestAccountStore_RequiredFields(t *testing.T) {
	srv := newTestService(t)
	store := newAccountStore(srv.db)
	ctx := context.Background()
	// Empty name → error.
	if _, err := store.Create(ctx, accountsIface.CreateAccountInput{Slug: "acme"}); err == nil {
		t.Fatalf("expected error for empty name")
	}
}

func TestPlanStore_RequiredFields(t *testing.T) {
	srv := newTestService(t)
	ctx := context.Background()
	acc, _ := newAccountStore(srv.db).Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	bs := newPlanStore(srv.db, acc.ID)

	// Empty name → error.
	if _, err := bs.Create(ctx, accountsIface.CreatePlanInput{Slug: "prod"}); err == nil {
		t.Fatalf("expected error for empty plan name")
	}
	// Bad slug → error.
	if _, err := bs.Create(ctx, accountsIface.CreatePlanInput{Slug: "BAD!", Name: "x"}); err == nil {
		t.Fatalf("expected error for bad plan slug")
	}
}

func TestPlanStore_UpdateAllFields(t *testing.T) {
	srv := newTestService(t)
	ctx := context.Background()
	acc, _ := newAccountStore(srv.db).Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	bs := newPlanStore(srv.db, acc.ID)
	b, _ := bs.Create(ctx, accountsIface.CreatePlanInput{Slug: "prod", Name: "Prod"})

	newName := "Production"
	newMode := accountsIface.PlanModeMetered
	newPeriod := "rolling-30d"
	newStatus := accountsIface.PlanStatusGrace
	upd, err := bs.Update(ctx, b.ID, accountsIface.UpdatePlanInput{
		Name:       &newName,
		Mode:       &newMode,
		Dimensions: []accountsIface.Dimension{{Name: "function.invocations"}},
		Period:     &newPeriod,
		Status:     &newStatus,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if upd.Name != newName || upd.Mode != newMode || upd.Period != newPeriod || upd.Status != newStatus {
		t.Fatalf("Update fields: %+v", upd)
	}
	if len(upd.Dimensions) != 1 || upd.Dimensions[0].Name != "function.invocations" {
		t.Fatalf("Update dimensions: %+v", upd.Dimensions)
	}
}

func TestUserStore_RequiredFields(t *testing.T) {
	srv := newTestService(t)
	ctx := context.Background()
	acc, _ := newAccountStore(srv.db).Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	us := newUserStore(srv.db, acc.ID)

	if _, err := us.Add(ctx, accountsIface.AddUserInput{Provider: "github"}); err == nil {
		t.Fatalf("expected error: missing external_id")
	}
	if _, err := us.Add(ctx, accountsIface.AddUserInput{ExternalID: "1"}); err == nil {
		t.Fatalf("expected error: missing provider")
	}
}

func TestUserStore_GrantUnknownPlan(t *testing.T) {
	srv := newTestService(t)
	ctx := context.Background()
	acc, _ := newAccountStore(srv.db).Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	us := newUserStore(srv.db, acc.ID)
	user, _ := us.Add(ctx, accountsIface.AddUserInput{Provider: "github", ExternalID: "1"})

	if err := us.Grant(ctx, user.ID, accountsIface.GrantPlanInput{PlanID: "ghost-plan"}); err == nil {
		t.Fatalf("expected error: granting unknown plan")
	}
	if err := us.Grant(ctx, "ghost-user", accountsIface.GrantPlanInput{PlanID: "x"}); err == nil {
		t.Fatalf("expected error: granting on unknown user")
	}
	if err := us.Grant(ctx, user.ID, accountsIface.GrantPlanInput{}); err == nil {
		t.Fatalf("expected error: empty plan id")
	}
}

func TestUserStore_RevokeMissingGrant(t *testing.T) {
	srv := newTestService(t)
	ctx := context.Background()
	acc, _ := newAccountStore(srv.db).Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	us := newUserStore(srv.db, acc.ID)
	user, _ := us.Add(ctx, accountsIface.AddUserInput{Provider: "github", ExternalID: "1"})
	if err := us.Revoke(ctx, user.ID, "ghost-plan"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestUserStore_List(t *testing.T) {
	srv := newTestService(t)
	ctx := context.Background()
	acc, _ := newAccountStore(srv.db).Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	us := newUserStore(srv.db, acc.ID)

	for i, ext := range []string{"1", "2", "3"} {
		_, err := us.Add(ctx, accountsIface.AddUserInput{Provider: "github", ExternalID: ext})
		if err != nil {
			t.Fatalf("Add %d: %v", i, err)
		}
	}
	ids, err := us.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("want 3 users, got %d (%v)", len(ids), ids)
	}
}

func TestMemberStore_UpdateRoleAndPasskeys(t *testing.T) {
	srv := newTestService(t)
	ctx := context.Background()
	acc, _ := newAccountStore(srv.db).Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	ms := newMemberStore(srv.db, acc.ID)

	m, err := ms.Invite(ctx, accountsIface.InviteMemberInput{PrimaryEmail: "alice@example.com"})
	if err != nil {
		t.Fatalf("Invite: %v", err)
	}

	// Update role.
	newRole := accountsIface.RoleViewer
	upd, err := ms.Update(ctx, m.ID, accountsIface.UpdateMemberInput{Role: &newRole})
	if err != nil || upd.Role != newRole {
		t.Fatalf("Update role: %v %+v", err, upd)
	}

	// Add a passkey, observe via Get.
	pk := accountsIface.PasskeyCredential{
		CredentialID:    []byte{1, 2, 3, 4},
		PublicKey:       []byte{0xa, 0xb, 0xc},
		AttestationType: "none",
	}
	if err := ms.AddPasskey(ctx, m.ID, pk); err != nil {
		t.Fatalf("AddPasskey: %v", err)
	}
	got, err := ms.Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.PasskeyCredentials) != 1 {
		t.Fatalf("want 1 passkey, got %d", len(got.PasskeyCredentials))
	}

	// AddPasskey requires non-empty CredentialID.
	if err := ms.AddPasskey(ctx, m.ID, accountsIface.PasskeyCredential{}); err == nil {
		t.Fatalf("expected error for empty CredentialID")
	}

	// RemovePasskey + Get → empty.
	if err := ms.RemovePasskey(ctx, m.ID, []byte{1, 2, 3, 4}); err != nil {
		t.Fatalf("RemovePasskey: %v", err)
	}
	got, _ = ms.Get(ctx, m.ID)
	if len(got.PasskeyCredentials) != 0 {
		t.Fatalf("want 0 passkeys after Remove, got %d", len(got.PasskeyCredentials))
	}
}

func TestMemberStore_ExternalIndex(t *testing.T) {
	srv := newTestService(t)
	ctx := context.Background()
	acc, _ := newAccountStore(srv.db).Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	ms := newMemberStore(srv.db, acc.ID)
	m, _ := ms.Invite(ctx, accountsIface.InviteMemberInput{PrimaryEmail: "alice@example.com"})

	if err := ms.AddExternalIndex(ctx, "okta", "subject-123", m.ID); err != nil {
		t.Fatalf("AddExternalIndex: %v", err)
	}
	idx, err := ms.readMemberIndex(ctx, LookupExternalPath("okta", "subject-123"))
	if err != nil || len(idx) != 1 {
		t.Fatalf("readMemberIndex: %v %+v", err, idx)
	}
	if err := ms.RemoveExternalIndex(ctx, "okta", "subject-123", m.ID); err != nil {
		t.Fatalf("RemoveExternalIndex: %v", err)
	}
	if _, err := ms.readMemberIndex(ctx, LookupExternalPath("okta", "subject-123")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected index empty after Remove, got %v", err)
	}
	// Removing again is a no-op (idempotent).
	if err := ms.RemoveExternalIndex(ctx, "okta", "subject-123", m.ID); err != nil {
		t.Fatalf("RemoveExternalIndex idempotent: %v", err)
	}
}

func TestInProcessClient_Accessors(t *testing.T) {
	srv := newTestService(t)
	cli := newInProcessClient(srv)
	if cli.Accounts() == nil {
		t.Fatalf("Accounts() nil")
	}
	if cli.Members("any") == nil {
		t.Fatalf("Members() nil")
	}
	if cli.Users("any") == nil {
		t.Fatalf("Users() nil")
	}
	if cli.Plans("any") == nil {
		t.Fatalf("Plans() nil")
	}
	if cli.Login() == nil {
		t.Fatalf("Login() nil")
	}
	// Peers returns the same client (no-op; phase 4 may scope).
	if cli.Peers() != cli {
		t.Fatalf("Peers() should be a no-op in-process")
	}
	cli.Close() // no panic
}

func TestInProcessClient_LoginStubsReturnError(t *testing.T) {
	srv := newTestService(t)
	cli := newInProcessClient(srv)
	login := cli.Login()
	ctx := context.Background()

	checks := []struct {
		name string
		fn   func() error
	}{
		{"StartManaged", func() error {
			_, err := login.StartManaged(ctx, accountsIface.StartManagedLoginInput{Email: "a@b"})
			return err
		}},
		{"FinishPasskey", func() error {
			_, err := login.FinishManagedPasskey(ctx, accountsIface.FinishPasskeyInput{})
			return err
		}},
		{"FinishMagicLink", func() error {
			_, err := login.FinishManagedMagicLink(ctx, accountsIface.FinishMagicLinkInput{})
			return err
		}},
		{"StartExternal", func() error {
			_, err := login.StartExternal(ctx, "acme")
			return err
		}},
		{"FinishExternal", func() error {
			_, err := login.FinishExternal(ctx, accountsIface.FinishExternalLoginInput{})
			return err
		}},
		{"VerifySession", func() error {
			_, err := login.VerifySession(ctx, "tok")
			return err
		}},
		{"Logout", func() error { return login.Logout(ctx, "tok") }},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if err := c.fn(); err == nil {
				t.Fatalf("%s should return errLoginNotImplemented", c.name)
			}
		})
	}
}

func TestVerify_RejectsEmptyArgs(t *testing.T) {
	srv := newTestService(t)
	cli := newInProcessClient(srv)
	ctx := context.Background()

	if _, err := cli.Verify(ctx, "", "x"); err == nil {
		t.Fatalf("expected error for empty provider")
	}
	if _, err := cli.Verify(ctx, "github", ""); err == nil {
		t.Fatalf("expected error for empty external_id")
	}
}

func TestMemberStore_ListAndInviteValidation(t *testing.T) {
	srv := newTestService(t)
	ctx := context.Background()
	acc, _ := newAccountStore(srv.db).Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	ms := newMemberStore(srv.db, acc.ID)

	// Empty email rejected.
	if _, err := ms.Invite(ctx, accountsIface.InviteMemberInput{}); err == nil {
		t.Fatalf("expected error for empty email")
	}

	// List grows with each invite.
	for _, e := range []string{"a@x", "b@x", "c@x"} {
		if _, err := ms.Invite(ctx, accountsIface.InviteMemberInput{PrimaryEmail: e}); err != nil {
			t.Fatalf("Invite %s: %v", e, err)
		}
	}
	ids, err := ms.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("want 3 members, got %d (%v)", len(ids), ids)
	}
}

func TestMemberStore_RemoveMissing(t *testing.T) {
	srv := newTestService(t)
	ctx := context.Background()
	acc, _ := newAccountStore(srv.db).Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	ms := newMemberStore(srv.db, acc.ID)
	if err := ms.Remove(ctx, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Remove missing should return ErrNotFound, got %v", err)
	}
}

func TestPackage_Factory(t *testing.T) {
	pkg := Package()
	if pkg == nil {
		t.Fatalf("Package() returned nil")
	}
}

func TestUserStore_GetByExternal_OtherAccount(t *testing.T) {
	// A git user can be a User on multiple Accounts. GetByExternal on Account A
	// must return ErrNotFound when the git user is only on Account B.
	srv := newTestService(t)
	ctx := context.Background()
	accA, _ := newAccountStore(srv.db).Create(ctx, accountsIface.CreateAccountInput{Slug: "alpha", Name: "Alpha"})
	accB, _ := newAccountStore(srv.db).Create(ctx, accountsIface.CreateAccountInput{Slug: "beta", Name: "Beta"})

	usB := newUserStore(srv.db, accB.ID)
	if _, err := usB.Add(ctx, accountsIface.AddUserInput{Provider: "github", ExternalID: "1"}); err != nil {
		t.Fatalf("Add B: %v", err)
	}

	usA := newUserStore(srv.db, accA.ID)
	if _, err := usA.GetByExternal(ctx, "github", "1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByExternal cross-account should ErrNotFound, got %v", err)
	}
}

// TestNew_MockedNode exercises the service.New / Close / Node / KV / Client
// wiring path using peer.Mock + mock KVDB. Mirrors the testing pattern used
// by services/auth/common_test.go, which lets us cover the service lifecycle
// without standing up a full dream universe in unit tests.
func TestNew_MockedNode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockNode := peer.Mock(ctx)
	defer mockNode.Close()

	cfg, err := tauConfig.New(
		tauConfig.WithRoot(t.TempDir()),
		tauConfig.WithNetworkFqdn("test.tau"),
		tauConfig.WithDevMode(true),
		tauConfig.WithP2PListen([]string{fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", 0)}),
		tauConfig.WithP2PAnnounce([]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 0)}),
		tauConfig.WithPrivateKey(keypair.NewRaw()),
		tauConfig.WithDomainValidation(tauConfig.DomainValidation{
			PrivateKey: []byte("k"),
			PublicKey:  []byte("k"),
		}),
		// No accounts.* needed — the relevant defaults (stdout email
		// fallback, accounts URL, WebAuthn RP) are all derived from
		// DevMode + NetworkFqdn at runtime.
		tauConfig.WithAccounts(tauConfig.Accounts{}),
	)
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	cfg.SetDatabases(mockkvdb.New())
	cfg.SetNode(mockNode)

	svc, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("accounts.New: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	// Service interface methods.
	if svc.Node() == nil {
		t.Fatalf("Node() nil")
	}
	if svc.KV() == nil {
		t.Fatalf("KV() nil")
	}
	cli := svc.Client()
	if cli == nil {
		t.Fatalf("Client() nil")
	}

	// Round-trip through the live Client to confirm the service initialised
	// the in-process Client with a working KVDB.
	acc, err := cli.Accounts().Create(ctx, accountsIface.CreateAccountInput{
		Slug: "acme", Name: "Acme",
	})
	if err != nil {
		t.Fatalf("Create via live service: %v", err)
	}
	got, err := cli.Accounts().Get(ctx, acc.ID)
	if err != nil || got.Slug != "acme" {
		t.Fatalf("Get via live service: %v %+v", err, got)
	}
}
