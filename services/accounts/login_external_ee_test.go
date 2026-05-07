//go:build ee

package accounts

import (
	"context"
	"strings"
	"testing"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
)

// ee build: external login routes through services/accounts/login_external_ee.go
// → ee/services/accounts/idp/oidc, which v1-stubs to "OIDC implementation
// not yet shipped".
func TestExternal_EEStubMessage(t *testing.T) {
	srv, _ := loginTestService(t)
	cli := newInProcessClient(srv)
	ctx := context.Background()

	_, err := cli.Login().StartExternal(ctx, "acme")
	if err == nil || !strings.Contains(err.Error(), "OIDC implementation not yet shipped") {
		t.Fatalf("EE StartExternal should say not-yet-shipped; got %v", err)
	}
	_, err = cli.Login().FinishExternal(ctx, accountsIface.FinishExternalLoginInput{Code: "x"})
	if err == nil || !strings.Contains(err.Error(), "OIDC implementation not yet shipped") {
		t.Fatalf("EE FinishExternal should say not-yet-shipped; got %v", err)
	}
}
