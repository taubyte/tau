//go:build !ee

package jobs

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/core/services/patrick"
	projectSchema "github.com/taubyte/tau/pkg/schema/project"

	"github.com/spf13/afero"
)

// fakeAccounts is a test double for accountsIface.Client. Monkey's binding check
// is build-agnostic — it hands the whole binding to Validate — so this fake only
// needs the base surface; the ee resolve behaviour is tested in the
// accounts packages, not here. Only Validate is wired.
type fakeAccounts struct {
	validateFn func(ctx context.Context, provider, externalID string, binding projectSchema.CloudBinding) (*accountsIface.ResolveResponse, error)
	calls      []string
}

func (f *fakeAccounts) Validate(ctx context.Context, provider, externalID string, binding projectSchema.CloudBinding) (*accountsIface.ResolveResponse, error) {
	f.calls = append(f.calls, binding.Account+"/"+binding.Plan+"|"+provider+"/"+externalID)
	if f.validateFn != nil {
		return f.validateFn(ctx, provider, externalID, binding)
	}
	return &accountsIface.ResolveResponse{Valid: true}, nil
}

func (f *fakeAccounts) Verify(context.Context, string, string) (*accountsIface.VerifyResponse, error) {
	return nil, errors.New("unused")
}
func (f *fakeAccounts) LookupAccountsByEmail(context.Context, string) ([]string, error) {
	return nil, errors.New("unused")
}
func (f *fakeAccounts) Accounts() accountsIface.Accounts          { return nil }
func (f *fakeAccounts) Members(string) accountsIface.Members      { return nil }
func (f *fakeAccounts) Users(string) accountsIface.Users          { return nil }
func (f *fakeAccounts) Login() accountsIface.Login                { return nil }
func (f *fakeAccounts) Peers(...peerCore.ID) accountsIface.Client { return f }
func (f *fakeAccounts) Close()                                    {}

const testFqdn = "tau-cloud.io"

// makeProject returns an in-memory Project with a clouds.<testFqdn>.{account, plan}
// binding when account+plan are given. Empty strings produce a binding-less project.
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

	return code{Context{
		ctx:         context.Background(),
		LogFile:     logFile,
		Job:         &patrick.Job{Meta: patrick.Meta{Repository: patrick.Repository{ID: 12345, Provider: "github"}}},
		Accounts:    ac,
		NetworkFqdn: testFqdn,
	}}
}

func TestCheckAccountBinding_Skipped_NoAccountsClient(t *testing.T) {
	c := newCodeCtx(t, nil)
	if err := c.checkAccountPlan(makeProject(t, "acme", "prod")); err != nil {
		t.Fatalf("expected skip with nil Accounts, got %v", err)
	}
}

func TestCheckAccountBinding_Skipped_NoBinding(t *testing.T) {
	fa := &fakeAccounts{}
	c := newCodeCtx(t, fa)
	if err := c.checkAccountPlan(makeProject(t, "", "")); err != nil {
		t.Fatalf("expected soft-skip for binding-less project, got %v", err)
	}
	if len(fa.calls) != 0 {
		t.Fatalf("expected no validate calls, got %d", len(fa.calls))
	}
}

func TestCheckAccountBinding_MissingAccount_Errors(t *testing.T) {
	fa := &fakeAccounts{}
	c := newCodeCtx(t, fa)
	if err := c.checkAccountPlan(makeProject(t, "", "prod")); err == nil {
		t.Fatalf("expected error when account is missing")
	} else if !strings.Contains(err.Error(), "account must be set") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheckAccountBinding_HandsWholeBindingToValidate(t *testing.T) {
	// The whole binding (account + plan) is forwarded to Validate — monkey
	// doesn't interpret the plan itself.
	fa := &fakeAccounts{}
	c := newCodeCtx(t, fa)
	if err := c.checkAccountPlan(makeProject(t, "acme", "prod")); err != nil {
		t.Fatalf("expected valid binding to pass, got %v", err)
	}
	if len(fa.calls) != 1 {
		t.Fatalf("expected 1 validate call, got %d", len(fa.calls))
	}
	if !strings.HasPrefix(fa.calls[0], "acme/prod|github/12345") {
		t.Fatalf("unexpected validate args: %s", fa.calls[0])
	}
}

func TestCheckAccountBinding_AccountOnly_Passes(t *testing.T) {
	fa := &fakeAccounts{}
	c := newCodeCtx(t, fa)
	if err := c.checkAccountPlan(makeProject(t, "acme", "")); err != nil {
		t.Fatalf("expected account-only binding to pass, got %v", err)
	}
	if len(fa.calls) != 1 || !strings.HasPrefix(fa.calls[0], "acme/|github/12345") {
		t.Fatalf("unexpected validate calls: %v", fa.calls)
	}
}

func TestCheckAccountBinding_Invalid_Fails(t *testing.T) {
	fa := &fakeAccounts{
		validateFn: func(context.Context, string, string, projectSchema.CloudBinding) (*accountsIface.ResolveResponse, error) {
			return &accountsIface.ResolveResponse{Valid: false, Reason: "git user not linked to account"}, nil
		},
	}
	c := newCodeCtx(t, fa)
	err := c.checkAccountPlan(makeProject(t, "acme", "prod"))
	if err == nil {
		t.Fatalf("expected invalid binding to fail compile")
	}
	if !strings.Contains(err.Error(), "git user not linked") {
		t.Fatalf("expected reason in error, got: %v", err)
	}
}

func TestCheckAccountBinding_ValidateError_Fails(t *testing.T) {
	fa := &fakeAccounts{
		validateFn: func(context.Context, string, string, projectSchema.CloudBinding) (*accountsIface.ResolveResponse, error) {
			return nil, errors.New("network unreachable")
		},
	}
	c := newCodeCtx(t, fa)
	err := c.checkAccountPlan(makeProject(t, "acme", "prod"))
	if err == nil {
		t.Fatalf("expected validate error to fail compile")
	}
	if !strings.Contains(err.Error(), "network unreachable") {
		t.Fatalf("expected wrapped error, got: %v", err)
	}
}

func TestCheckAccountBinding_NoGitProvider_Fails(t *testing.T) {
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
	if err := c.checkAccountPlan(makeProject(t, "acme", "prod")); err == nil {
		t.Fatalf("expected error when git provider id is missing")
	}
}

func TestCheckAccountBinding_DifferentCloud_Skipped(t *testing.T) {
	fa := &fakeAccounts{}
	c := newCodeCtx(t, fa)
	c.NetworkFqdn = "other-cloud.io"
	if err := c.checkAccountPlan(makeProject(t, "acme", "prod")); err != nil {
		t.Fatalf("expected skip when binding doesn't match this cloud, got %v", err)
	}
	if len(fa.calls) != 0 {
		t.Fatalf("expected no validate calls; got %d", len(fa.calls))
	}
}

func TestCheckAccountBinding_NoFqdn_Skipped(t *testing.T) {
	fa := &fakeAccounts{}
	c := newCodeCtx(t, fa)
	c.NetworkFqdn = ""
	if err := c.checkAccountPlan(makeProject(t, "acme", "prod")); err != nil {
		t.Fatalf("expected skip when NetworkFqdn is empty, got %v", err)
	}
	if len(fa.calls) != 0 {
		t.Fatalf("expected no validate calls; got %d", len(fa.calls))
	}
}
