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

	"github.com/libp2p/go-libp2p/core/pnet"
	"github.com/taubyte/tau/config"
	"github.com/urfave/cli/v2"
	"golang.org/x/exp/slices"
	"gopkg.in/yaml.v2"
)

// TODO: move to config as a methods

// Parse from yaml
func parseSourceConfig(ctx *cli.Context) (*config.Node, *config.Source, error) {
	root := ctx.Path("root")

	if !filepath.IsAbs(root) {
		return nil, nil, fmt.Errorf("root folder `%s` is not absolute", root)
	}

	configRoot := root + "/config"
	configPath := ctx.Path("path")
	if configPath != "" {
		configPath = path.Join(configRoot, ctx.String("shape")+".yaml")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("reading config file path `%s` failed with: %w", configPath, err)
	}

	src := &config.Source{}

	if err = yaml.Unmarshal(data, &src); err != nil {
		return nil, nil, fmt.Errorf("yaml unmarshal failed with: %w", err)
	}

	src.Domains.Key.Private = path.Join(configRoot, src.Domains.Key.Private)
	if src.Domains.Key.Public != "" {
		src.Domains.Key.Public = path.Join(configRoot, src.Domains.Key.Public)
	}
	src.Swarmkey = path.Join(configRoot, src.Swarmkey)

	err = validateKeys(src.Protocols, src.Domains.Key.Private, src.Domains.Key.Public)
	if err != nil {
		return nil, nil, err
	}

	protocol := &config.Node{
		Root:            root,
		Shape:           ctx.String("shape"),
		P2PAnnounce:     src.P2PAnnounce,
		P2PListen:       src.P2PListen,
		Ports:           src.Ports.ToMap(),
		Location:        src.Location,
		NetworkFqdn:     src.NetworkFqdn,
		GeneratedDomain: src.Domains.Generated,
		ServicesDomain:  src.Domains.Services,
		HttpListen:      "0.0.0.0:443",
		PrivateKey:      []byte(src.Privatekey),
		Protocols:       src.Protocols,
		Plugins:         src.Plugins,
		Peers:           src.Peers,
		DevMode:         ctx.Bool("dev-mode"),
	}

	// Convert Keys
	if len(src.Privatekey) > 0 {
		base64Key, err := base64.StdEncoding.DecodeString(src.Privatekey)
		if err != nil {
			return nil, nil, fmt.Errorf("converting private key to base 64 failed with: %s", err)
		}

		protocol.PrivateKey = []byte(base64Key)
	}

	if protocol.SwarmKey, err = parseSwarmKey(src.Swarmkey); err != nil {
		return nil, nil, err
	}

	if protocol.DomainValidation, err = parseValidationKey(&src.Domains.Key); err != nil {
		return nil, nil, err
	}

	return protocol, src, nil
}

func parseSwarmKey(filepath string) (pnet.PSK, error) {
	if len(filepath) > 0 {
		data, err := os.ReadFile(filepath)
		if err != nil {
			return nil, fmt.Errorf("reading %s failed with: %w", filepath, err)
		}

		return formatSwarmKey(string(data))
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
func validateKeys(protocols []string, privateKey, publicKey string) error {
	if slices.Contains(protocols, "auth") && privateKey == "" {
		return errors.New("domains private key cannot be empty when running auth")
	}

	for _, srv := range protocols {
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
