package auto

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"crypto/tls"

	basicHttp "github.com/taubyte/http/basic"
	"github.com/taubyte/http/options"
	authP2P "github.com/taubyte/tau/clients/p2p/auth"
	tnsP2P "github.com/taubyte/tau/clients/p2p/tns"
	"github.com/taubyte/tau/config"
	"github.com/taubyte/tau/p2p/peer"
	autoOptions "github.com/taubyte/tau/pkg/http-auto/options"

	acmeStore "github.com/taubyte/tau/services/auth/acme/store"

	commonSpecs "github.com/taubyte/tau/pkg/specs/common"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

func (s *Service) SetOption(opt interface{}) error {
	if opt == nil {
		return errors.New("`nil` option")
	}
	switch checker := opt.(type) {
	case autoOptions.OptionChecker:
		s.customDomainChecker = checker.Checker
	}
	// default: we ignore option we do not know so other modules can process them
	return nil
}

// Must listen on port 443
func New(node, clientNode peer.Node, config *config.Node, opts ...options.Option) (*Service, error) {
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

	s.certStore, err = acmeStore.New(clientNode.Context(), clientNode, cacheDir.Path(), autocert.ErrCacheMiss)
	if err != nil {
		logger.Error("new Auto HTTP: ", err)
		return nil, err
	}

	return &s, nil
}

func (s *Service) isProtocolOrAliasDomain(dom string) bool {
	if s.config.ServicesDomainRegExp.MatchString(dom) {
		return true
	}
	for _, r := range s.config.AliasDomainsRegExp {
		if r.MatchString(dom) {
			return true
		}
	}
	return false
}

// TODO: do a domain validation
func (s *Service) Start() {
	go func() {
		m := &autocert.Manager{
			Prompt: autocert.AcceptTOS,
			Cache:  s.certStore,
		}

		cfg := &tls.Config{
			GetCertificate: func(hello *tls.ClientHelloInfo) (cert *tls.Certificate, err error) {
				logger.Debugf("GetCertificate for %s from %s %v", hello.ServerName, hello.Conn.RemoteAddr(), hello.SupportedProtos)
				hello.ServerName = strings.ToLower(hello.ServerName)

				// Make sure its registered inside tns first and get projectID
				// Allow our services and our webhooks to bypass these checks but still check for everything else
				//TODO: better check later on but for now to not block our own console
				if s.customDomainChecker != nil {
					if !s.customDomainChecker(hello) {
						return nil, fmt.Errorf("customDomainChecker for %s was false", hello.ServerName)
					}
				} else if !s.isProtocolOrAliasDomain(hello.ServerName) {
					projectId, err := s.validateFromTns(hello.ServerName)
					if err != nil {
						return nil, fmt.Errorf("failed validateFromTns for %s with %v", hello.ServerName, err)
					}

					// Skips txt check if its using g.tau.link
					if !s.config.GeneratedDomainRegExp.MatchString(hello.ServerName) {
						if projectId == "" {
							return nil, fmt.Errorf("project ID is empty")
						}

						_, err = net.DefaultResolver.LookupTXT(s.Context(), projectId[:8]+"."+hello.ServerName)
						if err != nil {
							return nil, fmt.Errorf("failed txt lookup on %s with %v", hello.ServerName, err)
						}
					}
				} else {
					valid := false
					for _, proto := range commonSpecs.Services {
						if strings.HasPrefix(hello.ServerName, proto+".") {
							valid = true
							break
						}
					}
					if !valid {
						return nil, fmt.Errorf("invalid protocol in `%s`", hello.ServerName)
					}
				}

				logger.Debugf("looking for certificate for %s", hello.ServerName)
				cert, err = s.authClient.GetStaticCertificate(hello.ServerName)
				if err != nil {
					logger.Debugf("autocert will handle certificate for `%s`", hello.ServerName)
					cert, err = m.GetCertificate(hello)
					if err != nil {
						logger.Errorf("autocert getting certificate for `%s` failed: %s", hello.ServerName, err.Error())
						return nil, err
					}
					logger.Debugf("autocert got certificate for `%s` %v", hello.ServerName, cert)
				} else {
					logger.Debugf("got certificate for `%s` %v", hello.ServerName, hello.SupportedProtos)
				}

				return cert, nil
			},
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

		s.err = s.Server.Serve(lnTls)
		if s.err != http.ErrServerClosed {
			s.Kill()
		}
	}()
}

func (s *Service) GetListenAddress() (*url.URL, error) {
	return url.Parse("https://" + s.ListenAddress)
}

func (s *Service) Error() error {
	return s.err
}
