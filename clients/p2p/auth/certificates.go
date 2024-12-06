package auth

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"

	"github.com/taubyte/tau/p2p/streams/command"
	"github.com/taubyte/utils/maps"
)

// Injecting to /static
func (c *Client) InjectStaticCertificate(domain string, data []byte) error {
	_, err := c.client.Send("acme", command.Body{"action": "set-static", "fqdn": domain, "certificate": data}, c.peers...)
	if err != nil {
		return fmt.Errorf("failed sending inject certificate with %v", err)
	}

	return nil
}

func (c *Client) InjectKey(domain string, data []byte) error {
	_, err := c.client.Send("acme", command.Body{"action": "cache-set", "key": domain, "data": data}, c.peers...)
	if err != nil {
		return fmt.Errorf("failed sending inject key with %v", err)
	}

	return nil
}

func (c *Client) GetRawCertificate(domain string) ([]byte, error) {
	resp, err := c.client.Send("acme", command.Body{"action": "get", "fqdn": domain}, c.peers...)
	if err != nil {
		return nil, fmt.Errorf("failed get certificate for %s with %v", domain, err)
	}

	certData, err := maps.ByteArray(resp, "certificate")
	if err != nil {
		return nil, fmt.Errorf("failed finding certificate with %v", err)
	}

	return certData, nil
}

// Getting from /acme
func (c *Client) GetCertificate(domain string) (*tls.Certificate, error) {
	certData, err := c.GetRawCertificate(domain)
	if err != nil {
		return nil, err
	}

	return decodeX509(certData)
}

// Getting from /static
func (c *Client) GetRawStaticCertificate(domain string) ([]byte, error) {
	var err error
	if !strings.Contains(strings.Trim(domain, "."), ".") {
		return nil, errors.New("acme/autocert: server name component count invalid")
	}

	resp, err := c.client.Send("acme", command.Body{"action": "get-static", "fqdn": domain}, c.peers...)
	if err != nil {
		return nil, fmt.Errorf("failed get certificate for %s with %v", domain, err)
	}

	certData, err := maps.ByteArray(resp, "certificate")
	if err != nil {
		return nil, fmt.Errorf("failed finding certificate with %v", err)
	}

	return certData, nil
}

func (c *Client) GetStaticCertificate(domain string) (*tls.Certificate, error) {
	certData, err := c.GetRawStaticCertificate(domain)
	if err != nil {
		return nil, err
	}

	return decodeX509(certData)
}

func decodeX509(certData []byte) (cert *tls.Certificate, err error) {
	cert = &tls.Certificate{}
	for {
		block, rest := pem.Decode(certData)
		if block == nil {
			break
		}

		if block.Type == "CERTIFICATE" {
			cert.Certificate = append(cert.Certificate, block.Bytes)
		}

		if block.Type == "PRIVATE KEY" || strings.HasSuffix(block.Type, "PRIVATE KEY") {
			cert.PrivateKey, err = x509.ParsePKCS8PrivateKey(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("failed parsing private key with %v", err)
			}
		}
		certData = rest
	}
	return
}
