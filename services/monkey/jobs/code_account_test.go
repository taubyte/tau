package jobs

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/spf13/afero"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/core/services/patrick"
	projectSchema "github.com/taubyte/tau/pkg/schema/project"
)

// fakeAccounts is a minimal test double for accountsIface.Client. Only
// ResolvePlan is wired; the rest panic on use to surface accidental calls.
type fakeAccounts struct {
	resolveFn func(ctx context.Context, accountSlug, planSlug, provider, externalID string) (*accountsIface.ResolveResponse, error)
	calls     []string
}

func (f *fakeAccounts) ResolvePlan(ctx context.Context, accountSlug, planSlug, provider, externalID string) (*accountsIface.ResolveResponse, error) {
	f.calls = append(f.calls, accountSlug+"/"+planSlug+"|"+provider+"/"+externalID)
	if f.resolveFn != nil {
		return f.resolveFn(ctx, accountSlug, planSlug, provider, externalID)
	}
	return &accountsIface.ResolveResponse{Valid: true}, nil
}

func (f *fakeAccounts) Verify(context.Context, string, string) (*accountsIface.VerifyResponse, error) {
	return nil, errors.New("unused")
}
func (f *fakeAccounts) Accounts() accountsIface.Accounts          { return nil }
func (f *fakeAccounts) Members(string) accountsIface.Members      { return nil }
func (f *fakeAccounts) Users(string) accountsIface.Users          { return nil }
func (f *fakeAccounts) Plans(string) accountsIface.Plans          { return nil }
func (f *fakeAccounts) Login() accountsIface.Login                { return nil }
func (f *fakeAccounts) Peers(...peerCore.ID) accountsIface.Client { return f }
func (f *fakeAccounts) Close()                                    {}

const testFqdn = "tau-cloud.io"

// makeProject returns an in-memory Project with a clouds.<testFqdn>.{account, plan}
// binding when account+plan are given. Empty strings produce a binding-less project
// (the dream / local case).
func makeProject(t *testing.T, account, plan string) projectSchema.Project {
	t.Helper()
	p, err := projectSchema.Open(projectSchema.VirtualFS(afero.NewMemMapFs(), "/"))
	if err != nil {
		t.Fatalf("project open: %v", err)
	}
	if account != "" || plan != "" {
		if err := p.Set(true, projectSchema.CloudBindingOp(testFqdn, account, plan)); err != nil {
			t.Fatalf("project set: %v", err)
		}
	}
	return p
}

func newCodeCtx(t *testing.T, ac accountsIface.Client) code {
	t.Helper()
	logFile, err := os.CreateTemp("", "monkey-code-*.log")
	if err != nil {
		t.Fatalf("logfile: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(logFile.Name()); logFile.Close() })

	c := Context{
		ctx:         context.Background(),
		LogFile:     logFile,
		Job:         &patrick.Job{Meta: patrick.Meta{Repository: patrick.Repository{ID: 12345, Provider: "github"}}},
		Accounts:    ac,
		NetworkFqdn: testFqdn,
	}
	return code{c}
}

func TestCheckAccountPlan_Skipped_NoAccountsClient(t *testing.T) {
	c := newCodeCtx(t, nil)
	p := makeProject(t, "acme", "prod")
	if err := c.checkAccountPlan(p); err != nil {
		t.Fatalf("expected skip with nil Accounts, got %v", err)
	}
}

func TestCheckAccountPlan_Skipped_LegacyProject(t *testing.T) {
	fa := &fakeAccounts{}
	c := newCodeCtx(t, fa)
	p := makeProject(t, "", "")
	if err := c.checkAccountPlan(p); err != nil {
		t.Fatalf("expected legacy soft-skip, got %v", err)
	}
	if len(fa.calls) != 0 {
		t.Fatalf("expected no resolve calls for legacy project, got %d", len(fa.calls))
	}
}

func TestCheckAccountPlan_PartialDeclaration_Errors(t *testing.T) {
	fa := &fakeAccounts{}
	c := newCodeCtx(t, fa)
	p := makeProject(t, "acme", "")
	if err := c.checkAccountPlan(p); err == nil {
		t.Fatalf("expected error for partial declaration")
	} else if !strings.Contains(err.Error(), "must both be set or the entry must be omitted") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheckAccountPlan_ResolveValid(t *testing.T) {
	fa := &fakeAccounts{
		resolveFn: func(_ context.Context, _, _, _, _ string) (*accountsIface.ResolveResponse, error) {
			return &accountsIface.ResolveResponse{Valid: true}, nil
		},
	}
	c := newCodeCtx(t, fa)
	p := makeProject(t, "acme", "prod")
	if err := c.checkAccountPlan(p); err != nil {
		t.Fatalf("expected valid resolve to pass, got %v", err)
	}
	if len(fa.calls) != 1 {
		t.Fatalf("expected 1 resolve call, got %d", len(fa.calls))
	}
	if !strings.HasPrefix(fa.calls[0], "acme/prod|github/12345") {
		t.Fatalf("unexpected resolve args: %s", fa.calls[0])
	}
}

func TestCheckAccountPlan_ResolveInvalid_Fails(t *testing.T) {
	fa := &fakeAccounts{
		resolveFn: func(_ context.Context, _, _, _, _ string) (*accountsIface.ResolveResponse, error) {
			return &accountsIface.ResolveResponse{Valid: false, Reason: "plan suspended"}, nil
		},
	}
	c := newCodeCtx(t, fa)
	p := makeProject(t, "acme", "prod")
	err := c.checkAccountPlan(p)
	if err == nil {
		t.Fatalf("expected invalid resolve to fail compile")
	}
	if !strings.Contains(err.Error(), "plan suspended") {
		t.Fatalf("expected reason in error, got: %v", err)
	}
}

func TestCheckAccountPlan_ResolveError_Fails(t *testing.T) {
	fa := &fakeAccounts{
		resolveFn: func(_ context.Context, _, _, _, _ string) (*accountsIface.ResolveResponse, error) {
			return nil, errors.New("network unreachable")
		},
	}
	c := newCodeCtx(t, fa)
	p := makeProject(t, "acme", "prod")
	err := c.checkAccountPlan(p)
	if err == nil {
		t.Fatalf("expected resolve error to fail compile")
	}
	if !strings.Contains(err.Error(), "network unreachable") {
		t.Fatalf("expected wrapped error, got: %v", err)
	}
}

func TestCheckAccountPlan_NoGitProvider_Fails(t *testing.T) {
	fa := &fakeAccounts{}
	logFile, err := os.CreateTemp("", "monkey-code-*.log")
	if err != nil {
		t.Fatalf("logfile: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(logFile.Name()); logFile.Close() })

	// Job without repository ID → gitProviderIdentity returns empty externalID.
	c := code{Context{
		ctx:         context.Background(),
		LogFile:     logFile,
		Job:         &patrick.Job{},
		Accounts:    fa,
		NetworkFqdn: testFqdn,
	}}
	p := makeProject(t, "acme", "prod")
	if err := c.checkAccountPlan(p); err == nil {
		t.Fatalf("expected error when git provider id is missing")
	}
}

func TestCheckAccountPlan_DifferentCloud_Skipped(t *testing.T) {
	// Project declares a binding for cloud A; we're compiling for cloud B
	// → not deployed to this cloud → skip without calling ResolvePlan.
	fa := &fakeAccounts{}
	c := newCodeCtx(t, fa)
	c.NetworkFqdn = "other-cloud.io"
	p := makeProject(t, "acme", "prod") // binding under testFqdn ("tau-cloud.io")
	if err := c.checkAccountPlan(p); err != nil {
		t.Fatalf("expected skip when project's cloud binding doesn't match this cloud, got %v", err)
	}
	if len(fa.calls) != 0 {
		t.Fatalf("expected no resolve calls; got %d", len(fa.calls))
	}
}

func TestCheckAccountPlan_NoFqdn_Skipped(t *testing.T) {
	// monkey didn't propagate NetworkFqdn (defensive) → skip cleanly.
	fa := &fakeAccounts{}
	c := newCodeCtx(t, fa)
	c.NetworkFqdn = ""
	p := makeProject(t, "acme", "prod")
	if err := c.checkAccountPlan(p); err != nil {
		t.Fatalf("expected skip when NetworkFqdn is empty, got %v", err)
	}
	if len(fa.calls) != 0 {
		t.Fatalf("expected no resolve calls; got %d", len(fa.calls))
	}
}
