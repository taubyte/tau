package app

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"os"

	"github.com/libp2p/go-libp2p-core/pnet"
	"github.com/taubyte/odo/config"
	"github.com/urfave/cli/v2"
	"golang.org/x/exp/slices"
	"gopkg.in/yaml.v2"
)

func parseSourceConfig(ctx *cli.Context) (*config.Protocol, *config.Source, error) {
	// Parse from yaml
	if ctx.IsSet("config") {
		var (
			src      = new(config.Source)
			protocol = new(config.Protocol)
		)

		data, err := os.ReadFile(ctx.Path("config"))
		if err != nil {
			return nil, nil, fmt.Errorf("reading config file path `%s` failed with: %s", ctx.Path("config"), err)
		}

		err = yaml.Unmarshal(data, &src)
		if err != nil {
			return nil, nil, fmt.Errorf("yaml unmarshal failed with: %s", err)
		}

		err = validateKeys(src.Protocols, src.Domains.Key.Private, src.Domains.Key.Public)
		if err != nil {
			return nil, nil, err
		}

		// Assign basics
		protocol.Shape = ctx.String("shape")
		protocol.P2PAnnounce = src.P2PAnnounce
		protocol.P2PListen = src.P2PListen
		protocol.Ports = src.Ports
		protocol.Location = src.Location
		protocol.NetworkUrl = src.NetworkUrl
		protocol.GeneratedDomain = src.Domains.Generated
		protocol.ServicesDomain = src.Domains.Services
		protocol.PrivateKey = []byte(src.Privatekey)
		protocol.Protocols = src.Protocols
		protocol.Plugins = src.Plugins
		protocol.Peers = src.Peers

		protocol.DevMode = ctx.Bool("dev")

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

		protocol.DomainValidation.PrivateKey, protocol.DomainValidation.PublicKey, err = parseValidationKey(src.Domains.Key.Private, src.Domains.Key.Public)
		if err != nil {
			return nil, nil, err
		}

		return protocol, src, nil
	}

	return nil, nil, errors.New("config path was not set")
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
2. Monkey/Node either need a public key or a private key to generate a public key from
*/
func validateKeys(protocols []string, privateKey, publicKey string) error {
	if slices.Contains(protocols, "auth") && privateKey == "" {
		return errors.New("domains private key cannot be empty when running auth")
	}

	for _, srv := range protocols {
		if (srv == "monkey" || srv == "node") && (privateKey == "" && publicKey == "") {
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
