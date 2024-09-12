package domainLib

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"

	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/constants"
	"github.com/taubyte/utils/uri"
)

func ValidateCertificateKeyPairAndHostname(domain *structureSpec.Domain) ([]byte, []byte, error) {
	pair, err := tls.LoadX509KeyPair(domain.CertFile, domain.KeyFile)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load certificate and key file; %s", err)
	}

	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse certificate; %s", err)
	}

	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get system certificate pool; %s", err)
	}

	// Runs in testing only!
	if constants.SelfSignedOkay {
		if len(pair.Certificate) == 0 {
			return nil, nil, errors.New("no cert pairs found")
		}
		inter, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse intermediate certificate; %s", err)
		}
		roots.AddCert(inter)
	}

	opts := x509.VerifyOptions{
		DNSName: domain.Fqdn,
		Roots:   roots,
	}

	_, err = cert.Verify(opts)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to verify certificate; %s", err.Error())
	}

	// Convert Certificate files into bytes
	reader, err := uri.Open(domain.CertFile)
	if err != nil {
		return nil, nil, fmt.Errorf("failed opening file. %w", err)
	}
	defer reader.Close()
	certBytes, err := io.ReadAll(reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed reading file. %w", err)
	}

	//Convert Key files into bytes
	reader, err = uri.Open(domain.KeyFile)
	if err != nil {
		return nil, nil, fmt.Errorf("failed opening file. %w", err)
	}
	defer reader.Close()
	keyBytes, err := io.ReadAll(reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed reading file. %w", err)
	}
	return certBytes, keyBytes, nil
}
