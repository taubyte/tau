package app

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/pnet"
	"github.com/taubyte/tau/config"
	"github.com/urfave/cli/v2"
	"golang.org/x/exp/slices"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/go-seer"

	"github.com/taubyte/tau/utils"
)

// TODO: move to config as a methods

// Parse from yaml
func parseSourceConfig(ctx *cli.Context, shape string) (string, *config.Node, *config.Source, error) {
	root := ctx.Path("root")
	if root == "" {
		root = config.DefaultRoot
	}

	if !filepath.IsAbs(root) {
		return "", nil, nil, fmt.Errorf("root folder `%s` is not absolute", root)
	}

	configRoot := root + "/config"
	configPath := ctx.Path("path")
	if configPath == "" {
		configPath = path.Join(configRoot, shape+".yaml")
	}

	err := configMigration(seer.SystemFS(configRoot), shape)
	if err != nil {
		return "", nil, nil, fmt.Errorf("migration of `%s` failed with: %w", configPath, err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", nil, nil, fmt.Errorf("reading config file path `%s` failed with: %w", configPath, err)
	}

	src := &config.Source{}

	if err = yaml.Unmarshal(data, &src); err != nil {
		return "", nil, nil, fmt.Errorf("yaml unmarshal failed with: %w", err)
	}

	src.Domains.Key.Private = path.Join(configRoot, src.Domains.Key.Private)
	if src.Domains.Key.Public != "" {
		src.Domains.Key.Public = path.Join(configRoot, src.Domains.Key.Public)
	}
	src.Swarmkey = path.Join(configRoot, src.Swarmkey)

	err = validateKeys(src.Services, src.Domains.Key.Private, src.Domains.Key.Public)
	if err != nil {
		return "", nil, nil, err
	}

	cnf := &config.Node{
		Root:                  root,
		Shape:                 shape,
		P2PAnnounce:           src.P2PAnnounce,
		P2PListen:             src.P2PListen,
		Ports:                 src.Ports.ToMap(),
		Location:              src.Location,
		NetworkFqdn:           src.NetworkFqdn,
		GeneratedDomain:       src.Domains.Generated,
		GeneratedDomainRegExp: regexp.MustCompile(convertToPostfixRegex(src.Domains.Generated)),
		ServicesDomainRegExp:  regexp.MustCompile(convertToServicesRegex(src.NetworkFqdn)),
		AliasDomainsRegExp:    make([]*regexp.Regexp, 0),
		HttpListen:            "0.0.0.0:443",
		Services:              src.Services,
		Plugins:               src.Plugins,
		Peers:                 src.Peers,
		DevMode:               ctx.Bool("dev-mode"),
	}

	if src.Domains.Acme != nil && src.Domains.Acme.Url != "" {
		cnf.CustomAcme = true
		cnf.AcmeUrl = src.Domains.Acme.Url

		if src.Domains.Acme.CA != nil {
			cnf.AcmeCAInsecureSkipVerify = src.Domains.Acme.CA.SkipVerify
			if src.Domains.Acme.CA.RootCA != "" {
				caData, err := os.ReadFile(src.Domains.Acme.CA.RootCA)
				if err != nil {
					return "", nil, nil, fmt.Errorf("reading acme ca file failed with: %w", err)
				}
				cnf.AcmeRootCA = x509.NewCertPool()
				if !cnf.AcmeRootCA.AppendCertsFromPEM(caData) {
					return "", nil, nil, fmt.Errorf("failed to append acme ca")
				}
			}
		}

		if src.Domains.Acme.Key != "" {
			keyData, err := os.ReadFile(src.Domains.Acme.Key)
			if err != nil {
				return "", nil, nil, fmt.Errorf("reading acme key file failed with: %w", err)
			}

			keyBlock, _ := pem.Decode(keyData)
			if keyBlock == nil {
				return "", nil, nil, fmt.Errorf("failed to decode PEM block containing the key")
			}

			switch keyBlock.Type {
			case "RSA PRIVATE KEY":
				cnf.AcmeKey, err = x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
				if err != nil {
					return "", nil, nil, fmt.Errorf("parsing RSA private key failed with: %w", err)
				}
			case "EC PRIVATE KEY":
				cnf.AcmeKey, err = x509.ParseECPrivateKey(keyBlock.Bytes)
				if err != nil {
					return "", nil, nil, fmt.Errorf("parsing EC private key failed with: %w", err)
				}
			default:
				return "", nil, nil, fmt.Errorf("unsupported key type: %s", keyBlock.Type)
			}
		}
	}

	for _, d := range src.Domains.Aliases {
		cnf.AliasDomainsRegExp = append(cnf.AliasDomainsRegExp, regexp.MustCompile(convertToPostfixRegex(d)))
	}

	if len(src.Privatekey) == 0 {
		return "", nil, nil, errors.New("private key can not be empty")
	}

	base64Key, err := base64.StdEncoding.DecodeString(src.Privatekey)
	if err != nil {
		return "", nil, nil, fmt.Errorf("converting private key to base 64 failed with: %s", err)
	}

	cnf.PrivateKey = []byte(base64Key)

	if cnf.SwarmKey, err = parseSwarmKey(src.Swarmkey); err != nil {
		return "", nil, nil, err
	}

	if cnf.DomainValidation, err = parseValidationKey(&src.Domains.Key); err != nil {
		return "", nil, nil, err
	}

	pkey, err := crypto.UnmarshalPrivateKey(cnf.PrivateKey)
	if err != nil {
		return "", nil, nil, err
	}

	pid, err := peer.IDFromPublicKey(pkey.GetPublic())
	if err != nil {
		return "", nil, nil, err
	}

	return pid.String(), cnf, src, nil
}

func parseSwarmKey(filepath string) (pnet.PSK, error) {
	if len(filepath) > 0 {
		data, err := os.ReadFile(filepath)
		if err != nil {
			return nil, fmt.Errorf("reading %s failed with: %w", filepath, err)
		}

		return utils.FormatSwarmKey(string(data))
	}

	return nil, nil
}

func parseValidationKey(key *config.DVKey) (config.DomainValidation, error) {
	// Private Key
	privateKey, err := os.ReadFile(key.Private)
	if err != nil {
		return config.DomainValidation{}, fmt.Errorf("reading private key `%s` failed with: %s", key.Private, err)
	}

	// Public Key
	var publicKey []byte
	if key.Public != "" {
		publicKey, err = os.ReadFile(key.Public)
		if err != nil {
			return config.DomainValidation{}, fmt.Errorf("reading public key `%s` failed with: %w", key.Public, err)
		}
	} else {
		publicKey, err = generatePublicKey(privateKey)
		if err != nil {
			return config.DomainValidation{}, fmt.Errorf("generating public key failed with: %w", err)
		}
	}

	return config.DomainValidation{PrivateKey: privateKey, PublicKey: publicKey}, nil
}

/*
1. Auth needs private key to start properly
2. Monkey/Substrate either need a public key or a private key to generate a public key from
*/
func validateKeys(services []string, privateKey, publicKey string) error {
	if slices.Contains(services, "auth") && privateKey == "" {
		return errors.New("domains private key cannot be empty when running auth")
	}

	for _, srv := range services {
		if (srv == "monkey" || srv == "substrate") && (privateKey == "" && publicKey == "") {
			return errors.New("domains public key cannot be empty when running monkey or node")
		}
	}

	return nil
}

func generatePublicKey(privateKey []byte) ([]byte, error) {
	block, _ := pem.Decode(privateKey)
	if block == nil || block.Type != "EC PRIVATE KEY" {
		return nil, fmt.Errorf("failed to decode private key")
	}

	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing private key failed with: %w", err)
	}

	publicKeyDer, err := x509.MarshalPKIXPublicKey(key.Public())
	if err != nil {
		return nil, fmt.Errorf("marshalling PKIX pub key failed with: %w", err)
	}
	pubKeyBlock := pem.Block{
		Type:    "PUBLIC KEY",
		Headers: nil,
		Bytes:   publicKeyDer,
	}

	pubKeyPem := pem.EncodeToMemory(&pubKeyBlock)

	return pubKeyPem, nil
}
