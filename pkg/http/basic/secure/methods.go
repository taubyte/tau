package secure

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"

	"github.com/taubyte/tau/pkg/http/helpers"
	"github.com/taubyte/tau/pkg/http/options"
)

func (s *Service) SetOption(optIface interface{}) error {
	if optIface == nil {
		return errors.New("`nil` option")
	}

	var err error
	switch opt := optIface.(type) {
	case options.OptionSelfSignedCertificate:
		s.cert, s.key, err = helpers.GenerateCert(s.ListenAddress)
	case options.OptionLoadCertificate:
		s.cert, err = os.ReadFile(opt.CertificateFilename)
		if err == nil {
			s.key, err = os.ReadFile(opt.KeyFilename)
		}
	case options.OptionTryLoadCertificate:
		s.cert, err = os.ReadFile(opt.CertificateFilename)
		if err == nil {
			s.key, err = os.ReadFile(opt.KeyFilename)
		} else {
			s.cert, s.key, err = helpers.GenerateCert(s.ListenAddress)
		}
	}

	// default: we ignore option we do not know so other modules can process them
	return err
}

func (s *Service) Start() {
	go func() {
		_cert, err := tls.X509KeyPair(s.cert, s.key)
		if err != nil {
			s.err = fmt.Errorf("loading tls certificate/key failed with %w", err)
			s.Kill()
			return
		}

		s.Server.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{_cert},
		}

		s.err = s.Server.ListenAndServeTLS("", "")
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
