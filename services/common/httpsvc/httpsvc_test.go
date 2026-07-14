package httpsvc

import (
	"crypto"
	"io"
	"regexp"
	"testing"

	"github.com/taubyte/tau/pkg/config"
	autoOpts "github.com/taubyte/tau/pkg/http-auto/options"
	"github.com/taubyte/tau/pkg/http/options"
	"gotest.tools/v3/assert"
)

type optSink struct {
	listen           string
	clientNodeSet    bool
	autoTrustSet     bool
	skipProofSet     bool
	debugSet         bool
	acmeURL          string
	acmeCASkipVerify bool
	acmeRootsSet     bool
}

func (s *optSink) SetOption(o any) error {
	switch v := o.(type) {
	case options.OptionListen:
		s.listen = v.On
	case autoOpts.OptionClientNode:
		s.clientNodeSet = true
	case autoOpts.OptionAutoTrust:
		s.autoTrustSet = true
	case autoOpts.OptionSkipDomainProof:
		s.skipProofSet = true
	case options.OptionDebug:
		s.debugSet = true
	case options.OptionACME:
		s.acmeURL = v.DirectoryURL
	case options.OptionACMECASkipVerify:
		s.acmeCASkipVerify = v.Skip
	case options.OptionACMECARoots:
		s.acmeRootsSet = v.Roots != nil
	}
	return nil
}

func testCfg(t *testing.T, extra ...config.Option) config.Config {
	base := []config.Option{
		config.WithRoot("/tmp"),
		config.WithP2PListen([]string{"/ip4/0.0.0.0/tcp/0"}),
		config.WithP2PAnnounce([]string{"/ip4/127.0.0.1/tcp/0"}),
		config.WithPrivateKey(make([]byte, 32)),
		config.WithHttpListen("127.0.0.1:443"),
	}
	c, err := config.New(append(base, extra...)...)
	assert.NilError(t, err)
	return c
}

func TestAutoOptsFromConfig(t *testing.T) {
	cfg := testCfg(t,
		config.WithVerbose(true),
		config.WithCustomAcme(true),
		config.WithAcmeUrl("https://acme.example/directory"),
		config.WithAcmeKey(&fakeSigner{}),
		config.WithGeneratedDomainRegExp(regexp.MustCompile(`generated\.example\.com$`)),
		config.WithAliasDomainsRegExp([]*regexp.Regexp{regexp.MustCompile(`alias\.example\.com$`)}),
	)
	sink := &optSink{}
	for _, op := range AutoOptsFromConfig(cfg) {
		assert.NilError(t, op(sink))
	}
	assert.Equal(t, sink.listen, "127.0.0.1:443")
	assert.Assert(t, sink.clientNodeSet)
	assert.Assert(t, sink.autoTrustSet)
	assert.Assert(t, sink.skipProofSet)
	assert.Assert(t, sink.debugSet)
	assert.Equal(t, sink.acmeURL, "https://acme.example/directory")
}

func TestAutoTrustFromConfig(t *testing.T) {
	cfg := testCfg(t,
		config.WithNetworkFqdn("net.example.com"),
		config.WithAliasDomainsRegExp([]*regexp.Regexp{regexp.MustCompile(`^alias\.example\.com$`)}),
		config.WithServicesDomainRegExp(regexp.MustCompile(`^svc\.example\.com$`)),
		config.WithHosts(map[string]string{"admin.example.com": "gateway"}),
	)
	trust := autoTrustFromConfig(cfg)
	assert.Assert(t, trust("alias.example.com"))
	assert.Assert(t, trust("svc.example.com"))
	assert.Assert(t, trust("admin.example.com")) // custom domain bound via domains.hosts
	assert.Assert(t, !trust("random.example.com"))
	assert.Assert(t, trust("alias.example.com.")) // trailing dot tolerated
}

type fakeSigner struct{}

func (fakeSigner) Public() crypto.PublicKey { return nil }
func (fakeSigner) Sign(_ io.Reader, _ []byte, _ crypto.SignerOpts) ([]byte, error) {
	return nil, nil
}
