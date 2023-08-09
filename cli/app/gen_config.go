package app

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/go-interfaces/services/seer"
	"github.com/taubyte/p2p/keypair"
	"github.com/taubyte/tau/config"
	"github.com/urfave/cli/v2"
	"gopkg.in/yaml.v3"

	commonSpecs "github.com/taubyte/go-specs/common"
)

// TODO: move to config as a methods

func generateSourceConfig(ctx *cli.Context) (string, error) {
	root := ctx.Path("root")
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("root folder `%s` is not absolute", root)
	}

	nodeID, nodeKey, err := generateNodeKeyAndID()
	if err != nil {
		return "", err
	}

	mainP2pPort := ctx.Int("p2p-port")

	ips := ctx.StringSlice("ip")
	if len(ips) == 0 {
		ips = append(ips, "127.0.0.1")
	}

	announce := make([]string, len(ips))
	for i, ip := range ips {
		announce[i] = fmt.Sprintf("/ip4/%s/tcp/%d", ip, mainP2pPort)
	}

	configStruct := &config.Source{
		Privatekey:  nodeKey,
		Swarmkey:    path.Join("keys", "swarm.key"),
		Protocols:   getProtocols(ctx.String("protocols")),
		P2PListen:   []string{fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", mainP2pPort)},
		P2PAnnounce: announce,
		Ports: config.Ports{
			Main: mainP2pPort,
			Lite: mainP2pPort + 5,
			Ipfs: mainP2pPort + 10,
		},
		Location: &seer.Location{
			Latitude:  32.78306,
			Longitude: -96.80667,
		},
		Peers:       ctx.StringSlice("bootstrap"),
		NetworkFqdn: ctx.String("network"),
		Domains: config.Domains{
			Key: config.DVKey{
				Private: path.Join("keys", "dv_private.pem"),
				Public:  path.Join("keys", "dv_public.pem"),
			},
			Generated: regexp.QuoteMeta(fmt.Sprintf("g.%s", ctx.String("network"))) + `$`,
			Services:  `^[^.]+\.tau\.` + regexp.QuoteMeta(ctx.String("network")) + `$`,
		},
	}

	configRoot := root + "/config"
	if ctx.Bool("swarm-key") {
		swarmkey, err := generateSwarmKey()
		if err != nil {
			return "", err
		}

		if err = os.WriteFile(path.Join(configRoot, "keys", "swarm.key"), []byte(swarmkey), 0440); err != nil {
			return "", err
		}
	}

	if ctx.Bool("dv-keys") {
		priv, pub, err := generateDVKeys()
		if err != nil {
			return "", err
		}

		if err = os.WriteFile(path.Join(configRoot, "keys", "dv_private.pem"), priv, 0440); err != nil {
			return "", err
		}

		if err = os.WriteFile(path.Join(configRoot, "keys", "dv_public.pem"), pub, 0440); err != nil {
			return "", err
		}
	}

	configPath := path.Join(configRoot, ctx.String("shape")+".yaml")
	f, err := os.Create(configPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	yamlEnc := yaml.NewEncoder(f)
	if err = yamlEnc.Encode(configStruct); err != nil {
		return "", err
	}

	return nodeID, nil
}

func getProtocols(s string) []string {
	protos := make(map[string]bool)
	for _, p := range commonSpecs.Protocols {
		protos[p] = false
	}
	for _, p := range strings.Split(s, ",") {
		if _, ok := protos[p]; ok {
			protos[p] = true
		}
	}
	ret := make([]string, 0, len(commonSpecs.Protocols))
	for p, on := range protos {
		if on {
			ret = append(ret, p)
		}
	}
	return ret
}

func generateSwarmKey() (string, error) {
	key := make([]byte, 32)
	_, err := rand.Read(key)
	if err != nil {
		return "", fmt.Errorf("rand read failed with: %w", err)
	}

	return "/key/swarm/psk/1.0.0//base16/" + hex.EncodeToString(key), nil
}

func generateNodeKeyAndID() (string, string, error) {
	key := keypair.New()

	keyData, err := crypto.MarshalPrivateKey(key)
	if err != nil {
		return "", "", fmt.Errorf("marshal private key failed with %w", err)
	}

	id, err := peer.IDFromPublicKey(key.GetPublic())
	if err != nil {
		return "", "", fmt.Errorf("id from private key failed with %w", err)
	}

	return id.String(), base64.StdEncoding.EncodeToString(keyData), nil
}

func generateDVKeys() ([]byte, []byte, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate ecdsa key failed with %w", err)
	}

	privBytes, err := pemEncodePrivKey(priv)
	if err != nil {
		return nil, nil, err
	}

	pubBytes, err := pemEncodePubKey(priv)
	if err != nil {
		return nil, nil, err
	}

	return privBytes, pubBytes, nil
}

func pemEncodePrivKey(priv *ecdsa.PrivateKey) ([]byte, error) {
	privateKey, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("marshal private key failed with %w", err)
	}

	privateKeyPEM := &pem.Block{Type: "EC PRIVATE KEY", Bytes: privateKey}
	var privateBuf bytes.Buffer
	if err = pem.Encode(&privateBuf, privateKeyPEM); err != nil {
		return nil, fmt.Errorf("pem encode private key failed with %w", err)
	}

	return privateBuf.Bytes(), nil
}

func pemEncodePubKey(priv *ecdsa.PrivateKey) ([]byte, error) {
	// Generate public key and write to a file
	publicKey, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("marshal public key failed with %w", err)
	}

	publicKeyPEM := &pem.Block{Type: "PUBLIC KEY", Bytes: publicKey}
	var public bytes.Buffer
	if err = pem.Encode(&public, publicKeyPEM); err != nil {
		return nil, fmt.Errorf("failed encoding public key with %w", err)
	}

	return public.Bytes(), nil
}
