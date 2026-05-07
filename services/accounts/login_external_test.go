//go:build !ee

package accounts

import (
	"context"
	"strings"
	"testing"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
)

// !ee build: external login routes through services/accounts/login_external.go
// and must return the canonical "Enterprise Edition" guard message.
func TestExternal_CommunityMessage(t *testing.T) {
	srv, _ := loginTestService(t)
	cli := newInProcessClient(srv)
	ctx := context.Background()

	_, err := cli.Login().StartExternal(ctx, "acme")
	if err == nil || !strings.Contains(err.Error(), "Enterprise Edition") {
		t.Fatalf("StartExternal should mention Enterprise Edition; got %v", err)
	}
	_, err = cli.Login().FinishExternal(ctx, accountsIface.FinishExternalLoginInput{Code: "x"})
	if err == nil || !strings.Contains(err.Error(), "Enterprise Edition") {
		t.Fatalf("FinishExternal should mention Enterprise Edition; got %v", err)
	}
}
