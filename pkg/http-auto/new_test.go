package auto

import (
	"context"
	"testing"

	"github.com/taubyte/tau/p2p/peer"
	autoOpts "github.com/taubyte/tau/pkg/http-auto/options"
	"github.com/taubyte/tau/pkg/http/options"
	"gotest.tools/v3/assert"
)

func TestNew_PureOptions(t *testing.T) {
	ctx := context.Background()
	mockNode := peer.Mock(ctx)

	svc, err := New(ctx, mockNode,
		options.Listen("127.0.0.1:443"),
		autoOpts.CustomDomainChecker(func(string) bool { return true }),
		options.ACMEWithKey("https://acme-staging-v02.api.letsencrypt.org/directory", &MockSigner{}),
	)
	assert.NilError(t, err)
	assert.Assert(t, svc != nil)

	s := svc.(*Service)
	assert.Assert(t, s.certStore != nil)
	assert.Assert(t, s.authClient != nil)
	assert.Assert(t, s.tnsClient != nil)
	assert.Assert(t, s.positiveCache != nil)
	assert.Assert(t, s.negativeCache != nil)
	assert.Assert(t, s.customDomainChecker != nil)
}

func TestNew_RejectsNonHTTPSPort(t *testing.T) {
	ctx := context.Background()
	mockNode := peer.Mock(ctx)

	_, err := New(ctx, mockNode,
		options.Listen("127.0.0.1:8080"),
		autoOpts.CustomDomainChecker(func(string) bool { return true }),
	)
	assert.Assert(t, err != nil)
}
