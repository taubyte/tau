package config

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"

	"golang.org/x/exp/slices"

	"github.com/taubyte/tau/utils"
)

const defaultCAARecord = "letsencrypt.org"

// SourceOptions configures how a Source is applied when using WithSource.
type SourceOptions struct {
	Root    string
	Shape   string
	DevMode bool
}

// WithSource builds config from a Source (e.g. YAML). Resolves paths under root/config and loads keys from disk.
func WithSource(src *Source, opts SourceOptions) Option {
	return func(c *config) error {
		configRoot := path.Join(opts.Root, "config")
		privatePath := path.Join(configRoot, src.Domains.Key.Private)
		publicPath := src.Domains.Key.Public
		if publicPath != "" {
			publicPath = path.Join(configRoot, publicPath)
		}
		swarmPath := src.Swarmkey
		if swarmPath != "" {
			swarmPath = path.Join(configRoot, swarmPath)
		}

		if err := validateSourceKeys(src.Services, src.Domains.Key.Private, src.Domains.Key.Public); err != nil {
			return err
		}

		if len(src.Privatekey) == 0 {
			return fmt.Errorf("private key cannot be empty")
		}
		keyBytes, err := base64.StdEncoding.DecodeString(src.Privatekey)
		if err != nil {
			return fmt.Errorf("decoding private key: %w", err)
		}
		c.privateKey = keyBytes

		c.root = opts.Root
		c.shape = opts.Shape
		c.devMode = opts.DevMode
		c.services = src.Services
		c.cluster = src.Cluster
		c.p2pListen = src.P2PListen
		c.p2pAnnounce = src.P2PAnnounce
		c.ports = src.Ports.ToMap()
		c.location = src.Location
		c.networkFqdn = src.NetworkFqdn
		c.generatedDomain = src.Domains.Generated
		c.generatedDomainRegExp = regexp.MustCompile(convertToPostfixRegex(src.Domains.Generated))
		c.servicesDomainRegExp = regexp.MustCompile(convertToServicesRegex(src.NetworkFqdn))
		c.aliasDomainsRegExp = make([]*regexp.Regexp, 0)
		c.httpListen = DefaultHTTPListen
		c.plugins = src.Plugins
		c.peers = src.Peers
		c.acmeCAARecord = defaultCAARecord

		if c.swarmKey, err = loadSwarmKey(swarmPath); err != nil {
			return err
		}
		if c.domainValidation, err = loadValidationKey(privatePath, publicPath); err != nil {
			return err
		}

		if src.Domains.Acme != nil && src.Domains.Acme.Url != "" {
			c.customAcme = true
			c.acmeUrl = src.Domains.Acme.Url
			if src.Domains.Acme.CA != nil {
				c.acmeCAInsecureSkipVerify = src.Domains.Acme.CA.SkipVerify
				if src.Domains.Acme.CA.RootCA != "" {
					caPath := path.Join(configRoot, src.Domains.Acme.CA.RootCA)
					caData, err := os.ReadFile(caPath)
					if err != nil {
						return fmt.Errorf("reading acme ca: %w", err)
					}
					c.acmeRootCA = x509.NewCertPool()
					if !c.acmeRootCA.AppendCertsFromPEM(caData) {
						return fmt.Errorf("failed to append acme ca")
					}
				}
				if src.Domains.Acme.CA.CAARecord != "" {
					c.acmeCAARecord = src.Domains.Acme.CA.CAARecord
				}
			}
			if src.Domains.Acme.Key != "" {
				keyPath := path.Join(configRoot, src.Domains.Acme.Key)
				keyData, err := os.ReadFile(keyPath)
				if err != nil {
					return fmt.Errorf("reading acme key: %w", err)
				}
				keyBlock, _ := pem.Decode(keyData)
				if keyBlock == nil {
					return fmt.Errorf("failed to decode PEM block for acme key")
				}
				switch keyBlock.Type {
				case "RSA PRIVATE KEY":
					c.acmeKey, err = x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
				case "EC PRIVATE KEY":
					c.acmeKey, err = x509.ParseECPrivateKey(keyBlock.Bytes)
				default:
					return fmt.Errorf("unsupported acme key type: %s", keyBlock.Type)
				}
				if err != nil {
					return fmt.Errorf("parsing acme key: %w", err)
				}
			}
		}

		for _, d := range src.Domains.Aliases {
			c.aliasDomainsRegExp = append(c.aliasDomainsRegExp, regexp.MustCompile(convertToPostfixRegex(d)))
		}

		return nil
	}
}

func validateSourceKeys(services []string, privateKey, publicKey string) error {
	if slices.Contains(services, "auth") && privateKey == "" {
		return fmt.Errorf("domains private key cannot be empty when running auth")
	}
	for _, srv := range services {
		if (srv == "monkey" || srv == "substrate") && privateKey == "" && publicKey == "" {
			return fmt.Errorf("domains public key cannot be empty when running monkey or node")
		}
	}
	return nil
}

func loadSwarmKey(filePath string) ([]byte, error) {
	if filePath == "" {
		return nil, nil
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("reading swarm key: %w", err)
	}
	psk, err := utils.FormatSwarmKey(string(data))
	if err != nil {
		return nil, err
	}
	return psk, nil
}

func loadValidationKey(privatePath, publicPath string) (DomainValidation, error) {
	privateKey, err := os.ReadFile(privatePath)
	if err != nil {
		return DomainValidation{}, fmt.Errorf("reading domain private key %s: %w", privatePath, err)
	}
	var publicKey []byte
	if publicPath != "" {
		publicKey, err = os.ReadFile(publicPath)
		if err != nil {
			return DomainValidation{}, fmt.Errorf("reading domain public key %s: %w", publicPath, err)
		}
	} else {
		publicKey, err = generatePublicKeyFromPEM(privateKey)
		if err != nil {
			return DomainValidation{}, fmt.Errorf("generating domain public key: %w", err)
		}
	}
	return DomainValidation{PrivateKey: privateKey, PublicKey: publicKey}, nil
}

func generatePublicKeyFromPEM(privateKey []byte) ([]byte, error) {
	block, _ := pem.Decode(privateKey)
	if block == nil || block.Type != "EC PRIVATE KEY" {
		return nil, fmt.Errorf("failed to decode EC private key")
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing private key: %w", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(key.Public())
	if err != nil {
		return nil, fmt.Errorf("marshalling public key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}), nil
}

func convertToPostfixRegex(url string) string {
	return `^[^.]+\.` + strings.Join(strings.Split(url, "."), `\.`) + "$"
}

func convertToServicesRegex(url string) string {
	return `^[^.]+\.tau\.` + strings.Join(strings.Split(url, "."), `\.`) + `$`
}
