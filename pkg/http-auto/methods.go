package auto

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"crypto/tls"

	"github.com/jellydator/ttlcache/v3"
	authP2P "github.com/taubyte/tau/clients/p2p/auth"
	tnsP2P "github.com/taubyte/tau/clients/p2p/tns"
	"github.com/taubyte/tau/config"
	"github.com/taubyte/tau/p2p/peer"
	autoOptions "github.com/taubyte/tau/pkg/http-auto/options"
	basicHttp "github.com/taubyte/tau/pkg/http/basic"
	"github.com/taubyte/tau/pkg/http/options"

	acmeStore "github.com/taubyte/tau/services/auth/acme/store"

	commonSpecs "github.com/taubyte/tau/pkg/specs/common"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
	"golang.org/x/exp/slices"
)

func (s *Service) SetOption(opt interface{}) error {
	if opt == nil {
		return errors.New("`nil` option")
	}
	switch opt := opt.(type) {
	case autoOptions.OptionChecker:
		s.customDomainChecker = opt.Checker
	case options.OptionACME:
		s.acme = &opt
	}
	// default: we ignore option we do not know so other modules can process them
	return nil
}

// Must listen on port 443
func new(node, clientNode peer.Node, config *config.Node, opts ...options.Option) (*Service, error) {
	logger.Debug("New Auto HTTP")
	defer logger.Debug("New Auto HTTP -> done")
	_s, err := basicHttp.New(node.Context(), opts...)
	if err != nil {
		logger.Error("New Auto HTTP: ", err)
		return nil, err
	}

	_, _port, err := net.SplitHostPort(_s.ListenAddress)
	if err != nil {
		logger.Error("New Auto HTTP address ", _s.ListenAddress, ": ", err)
		return nil, err
	}

	if _port != "443" && _port != "https" {
		err = fmt.Errorf("address %s using invalid port. Should be 443", _s.ListenAddress)
		logger.Error("New Auto HTTP: ", err)
		return nil, err
	}

	// For non-odo pushes
	if clientNode == nil {
		clientNode = node
	}

	var s Service
	s.Service = _s

	s.config = config

	err = options.Parse(&s, opts)
	if err != nil {
		return nil, err
	}

	s.authClient, err = authP2P.New(s.Context(), clientNode)
	if err != nil {
		return nil, fmt.Errorf("failed creating auth client with %v", err)
	}

	s.tnsClient, err = tnsP2P.New(s.Context(), clientNode)
	if err != nil {
		return nil, fmt.Errorf("failed creating tns client with %v", err)
	}

	cacheDir, err := clientNode.NewFolder("acme")
	if err != nil {
		logger.Error("creating acme cache foler failed with ", err)
		return nil, err
	}

	s.certStore, err = acmeStore.New(clientNode.Context(), clientNode, cacheDir.Path())
	if err != nil {
		logger.Error("new Auto HTTP: ", err)
		return nil, err
	}

	s.positiveCache = ttlcache.New(ttlcache.WithTTL[string, bool](PositiveTTL))
	s.negativeCache = ttlcache.New(ttlcache.WithTTL[string, bool](NegativeTTL))

	return &s, nil
}

func (s *Service) isServiceOrAliasDomain(dom string) bool {
	dom = strings.TrimSuffix(dom, ".")

	if slices.ContainsFunc(commonSpecs.Services, func(srv string) bool {
		return dom == srv+"."+s.config.NetworkFqdn
	}) {
		return true
	}

	for _, r := range s.config.AliasDomainsRegExp {
		if r.MatchString(dom) {
			return true
		}
	}

	return s.config.ServicesDomainRegExp.MatchString(dom)
}

func (s *Service) validateFQDN(host string) error {
	if item := s.negativeCache.Get(host); item != nil && item.Value() {
		return fmt.Errorf("cached as invalid: %s", host)
	}

	if item := s.positiveCache.Get(host); item != nil && item.Value() {
		return nil
	}

	projectId, err := s.validateFromTns(host)
	if err != nil {
		s.negativeCache.Set(host, true, NegativeTTL)
		return fmt.Errorf("failed validateFromTns for %s with %v", host, err) // Validation failed, return error
	}

	// Check txt if it not using generated domain
	if !s.config.GeneratedDomainRegExp.MatchString(host) {
		if projectId == "" {
			s.negativeCache.Set(host, true, NegativeTTL)
			return fmt.Errorf("project ID is empty") // Project ID is empty, return error
		}

		_, err = net.DefaultResolver.LookupTXT(s.Context(), projectId[:8]+"."+host)
		if err != nil {
			s.negativeCache.Set(host, true, NegativeTTL)
			return fmt.Errorf("failed txt lookup on %s with %v", host, err) // TXT lookup failed, return error
		}
	}

	s.positiveCache.Set(host, true, PositiveTTL)
	return nil
}

func (s *Service) Start() {
	go s.positiveCache.Start()
	go s.negativeCache.Start()

	m := &autocert.Manager{
		Prompt: autocert.AcceptTOS,
		Cache:  s.certStore,
		HostPolicy: func(ctx context.Context, host string) error {
			if host == "" {
				return fmt.Errorf("server name is empty")
			}

			logger.Debugf("HostPolicy for %s", host)
			host = strings.ToLower(host)

			if s.customDomainChecker != nil {
				if !s.customDomainChecker(host) {
					return fmt.Errorf("customDomainChecker for %s was false", host)
				}
			} else if !s.isServiceOrAliasDomain(host) {
				if err := s.validateFQDN(host); err != nil {
					return err
				}
			}

			return nil
		},
	}

	if s.acme != nil {
		m.Client = &acme.Client{
			DirectoryURL: s.acme.DirectoryURL,
			Key:          s.acme.Key,
		}

		if s.config.AcmeCAInsecureSkipVerify || s.config.AcmeRootCA != nil {
			m.Client.HTTPClient = &http.Client{
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{
						InsecureSkipVerify: s.config.AcmeCAInsecureSkipVerify,
						RootCAs:            s.config.AcmeRootCA,
					},
				},
			}
		}
	}

	cfg := &tls.Config{
		GetCertificate: m.GetCertificate,
		NextProtos: []string{
			"http/1.1", acme.ALPNProto,
		},
	}

	// Let's Encrypt tls-alpn-01 only works on port 443.
	ln, err := net.Listen("tcp4", s.ListenAddress) /* #nosec G102 */
	if err != nil {
		s.err = fmt.Errorf("creating TLS listener failed with %w", err)
		s.Kill()
		return
	}

	lnTls := tls.NewListener(ln, cfg)

	go func() {
		s.err = s.Server.Serve(lnTls)
		if s.err != http.ErrServerClosed {
			s.Kill()
		}
		s.positiveCache.Stop()
		s.negativeCache.Stop()
	}()
}

func (s *Service) GetListenAddress() (*url.URL, error) {
	return url.Parse("https://" + s.ListenAddress)
}

func (s *Service) Error() error {
	return s.err
}
