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

// Parse from yaml
func parseSourceConfig(ctx *cli.Context) (*config.Protocol, *config.Source, error) {
	root := ctx.Path("root")

	if !filepath.IsAbs(root) {
		return nil, nil, fmt.Errorf("root folder `%s` is not absolute", root)
	}

	configRoot := root + "/config"
	configPath := path.Join(configRoot, ctx.String("shape")+".yaml")

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("reading config file path `%s` failed with: %s", configPath, err)
	}

	src := &config.Source{}

	err = yaml.Unmarshal(data, &src)
	if err != nil {
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

	protocol := &config.Protocol{
		Root:            root,
		Shape:           ctx.String("shape"),
		P2PAnnounce:     src.P2PAnnounce,
		P2PListen:       src.P2PListen,
		Ports:           src.Ports,
		Location:        src.Location,
		NetworkUrl:      src.NetworkUrl,
		GeneratedDomain: src.Domains.Generated,
		ServicesDomain:  src.Domains.Services,
		HttpListen:      src.HttpListen,
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

	protocol.SwarmKey, err = parseSwarmKey(src.Swarmkey)
	if err != nil {
		return nil, nil, err
	}

	protocol.DomainValidation.PrivateKey, protocol.DomainValidation.PublicKey, err = parseValidationKey(
		src.Domains.Key.Private,
		src.Domains.Key.Public,
	)
	if err != nil {
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

func parseValidationKey(privateKeyPath, publicKeyPath string) ([]byte, []byte, error) {
	// Private Key
	privateKey, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("reading private key `%s` failed with: %s", privateKeyPath, err)
	}

	// Public Key
	var publicKey []byte
	if publicKeyPath != "" {
		publicKey, err = os.ReadFile(publicKeyPath)
		if err != nil {
			return nil, nil, fmt.Errorf("reading public key `%s` failed with: %s", publicKeyPath, err)
		}
	} else {
		publicKey, err = generatePublicKey(privateKey)
		if err != nil {
			return nil, nil, fmt.Errorf("generating public key failed with: %s", err)
		}
	}

	return privateKey, publicKey, nil
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
		return nil, fmt.Errorf("parsing private key failed with: %s", err)
	}

	publicKeyDer, err := x509.MarshalPKIXPublicKey(key.Public())
	if err != nil {
		return nil, fmt.Errorf("marshalling PKIX pub key failed with: %s", err)
	}
	pubKeyBlock := pem.Block{
		Type:    "PUBLIC KEY",
		Headers: nil,
		Bytes:   publicKeyDer,
	}

	pubKeyPem := pem.EncodeToMemory(&pubKeyBlock)

	return pubKeyPem, nil
}
