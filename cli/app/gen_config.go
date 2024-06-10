package app

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/config"
	"github.com/taubyte/tau/core/services/seer"
	"github.com/taubyte/tau/p2p/keypair"
	"github.com/taubyte/tau/utils"
	"github.com/urfave/cli/v2"
	"gopkg.in/yaml.v3"

	commonSpecs "github.com/taubyte/tau/pkg/specs/common"

	"github.com/pterm/pterm"

	jwt "github.com/dgrijalva/jwt-go"
)

// TODO: move to config as a methods

func generateSourceConfig(ctx *cli.Context) error {
	root := ctx.Path("root")
	if !filepath.IsAbs(root) {
		return fmt.Errorf("root folder `%s` is not absolute", root)
	}

	shape := ctx.String("shape")

	var (
		passwd string
		err    error

		skdata  []byte
		dvsdata []byte
		dvpdata []byte
		pkey    string
	)

	templatePath := ctx.Path("use")
	var bundle *config.Bundle
	if templatePath != "" {
		if f, err := os.Open(templatePath); err != nil {
			return fmt.Errorf("failed to read template %s with %w", templatePath, err)
		} else {
			ydec := yaml.NewDecoder(f)
			bundle = &config.Bundle{}
			err = ydec.Decode(bundle)
			f.Close()
			if err != nil {
				return fmt.Errorf("failed to parse template %s with %w", templatePath, err)
			}

			pkey = bundle.Privatekey
		}
	}

	if shape == "" && bundle != nil {
		shape = bundle.Origin.Shape
	}

	if bundle != nil && bundle.Origin.Protected {
		if passwd, err = promptPassword("Password?"); err != nil {
			return fmt.Errorf("faild to read password with %w", err)
		} else {

			if pkey != "" {
				if pkdata, err := base64.StdEncoding.DecodeString(pkey); err != nil {
					return fmt.Errorf("faild to read encrypted private key with %w", err)
				} else if pkdata, err = decrypt(pkdata, passwd); err != nil {
					return fmt.Errorf("faild to decrypt private key with %w", err)
				} else {
					pkey = string(pkdata)
				}
			}

			if skdata, err = base64.StdEncoding.DecodeString(bundle.Swarmkey); err != nil {
				return fmt.Errorf("faild to read encrypted swarm key with %w", err)
			} else if skdata, err = decrypt(skdata, passwd); err != nil {
				return fmt.Errorf("faild to encrypt swarm key with %w", err)
			}

			if dvsdata, err = base64.StdEncoding.DecodeString(bundle.Domains.Key.Private); err != nil {
				return fmt.Errorf("faild to read encrypted domain key with %w", err)
			} else if dvsdata, err = decrypt(dvsdata, passwd); err != nil {
				return fmt.Errorf("faild to encrypt domain key with %w", err)
			}

			if bundle.Domains.Key.Public != "" {
				if dvpdata, err = base64.StdEncoding.DecodeString(bundle.Domains.Key.Public); err != nil {
					return fmt.Errorf("faild to read encrypted domain public key with %w", err)
				} else if dvpdata, err = decrypt(dvpdata, passwd); err != nil {
					return fmt.Errorf("faild to encrypt domain public key with %w", err)
				}
			}
		}
	}

	nodeID, nodeKey, err := generateNodeKeyAndID(pkey)
	if err != nil {
		return err
	}

	var ports config.Ports
	ports.Main = ctx.Int("p2p-port")
	ports.Lite = ports.Main + 5
	ports.Ipfs = ports.Main + 10

	if bundle != nil && ports.Main == 4242 {
		ports.Main = bundle.Ports.Main
		if bundle.Ports.Lite != 0 {
			ports.Lite = bundle.Ports.Lite
		} else {
			ports.Lite = ports.Main + 5
		}
		if bundle.Ports.Ipfs != 0 {
			ports.Ipfs = bundle.Ports.Ipfs
		} else {
			ports.Ipfs = ports.Main + 10
		}
	}

	var announce []string
	ips := ctx.StringSlice("ip")
	if len(ips) > 0 {
		announce = make([]string, len(ips))
		for i, ip := range ips {
			announce[i] = fmt.Sprintf("/ip4/%s/tcp/%d", ip, ports.Main)
		}
	} else if bundle != nil {
		announce = bundle.P2PAnnounce
	} else {
		announce = []string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", ports.Main)}
	}

	Services := getServices(ctx.String("Services"))
	if len(Services) == 0 && bundle != nil {
		Services = bundle.Services
	}

	p2pListen := []string{fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", ports.Main)}
	if bundle != nil && len(bundle.P2PListen) > 0 {
		p2pListen = bundle.P2PListen
	}

	var location *seer.Location
	if bundle != nil && bundle.Location != nil {
		location = bundle.Location
	} else {
		location, err = estimateGPSLocation()
		if err != nil {
			return fmt.Errorf("extimating GPS location failed with %w", err)
		}
	}

	peers := ctx.StringSlice("bootstrap")
	if len(peers) == 0 && bundle != nil {
		peers = bundle.Peers
	}

	fqdn := ctx.String("network")
	genfqdn := fmt.Sprintf("g.%s", ctx.String("network"))
	if len(fqdn) == 0 && bundle != nil {
		fqdn = bundle.NetworkFqdn
		genfqdn = bundle.Domains.Generated
	}

	configStruct := &config.Source{
		Privatekey:  nodeKey,
		Swarmkey:    path.Join("keys", "swarm.key"),
		Services:    Services,
		P2PListen:   p2pListen,
		P2PAnnounce: announce,
		Ports:       ports,
		Location:    location,
		Peers:       peers,
		NetworkFqdn: fqdn,
		Domains: config.Domains{
			Key: config.DVKey{
				Private: path.Join("keys", "dv_private.pem"),
				Public:  path.Join("keys", "dv_public.pem"),
			},
			Generated: genfqdn,
		},
	}

	configRoot := root + "/config"

	if err = os.MkdirAll(path.Join(configRoot, "keys"), 0750); err != nil {
		return err
	}

	if ctx.Bool("swarm-key") || len(skdata) > 0 {
		swarmkey, err := generateSwarmKey(skdata)
		if err != nil {
			return err
		}

		if err = os.WriteFile(path.Join(configRoot, "keys", "swarm.key"), []byte(swarmkey), 0640); err != nil {
			return fmt.Errorf("failed to write config file with %w", err)
		}
	}

	if ctx.Bool("dv-keys") || len(dvsdata) > 0 {
		priv, pub, err := generateDVKeys(dvsdata, dvpdata)
		if err != nil {
			return err
		}

		if err = os.WriteFile(path.Join(configRoot, "keys", "dv_private.pem"), priv, 0640); err != nil {
			return err
		}

		if err = os.WriteFile(path.Join(configRoot, "keys", "dv_public.pem"), pub, 0640); err != nil {
			return err
		}
	}

	configPath := path.Join(configRoot, shape+".yaml")
	f, err := os.Create(configPath)
	if err != nil {
		return err
	}
	defer f.Close()

	yamlEnc := yaml.NewEncoder(f)
	if err = yamlEnc.Encode(configStruct); err != nil {
		return err
	}

	pterm.Info.Println("ID:", nodeID)

	return nil
}

func getServices(s string) []string {
	if s == "all" {
		return append([]string{}, commonSpecs.Services...)
	}

	protos := make(map[string]bool)
	for _, p := range commonSpecs.Services {
		protos[p] = false
	}
	for _, p := range strings.Split(s, ",") {
		if _, ok := protos[p]; ok {
			protos[p] = true
		}
	}
	ret := make([]string, 0, len(commonSpecs.Services))
	for p, on := range protos {
		if on {
			ret = append(ret, p)
		}
	}
	return ret
}

func generateSwarmKey(data []byte) (string, error) {
	if len(data) > 0 {
		return string(data), nil
	}

	return utils.GenerateSwarmKey()
}

func generateNodeKeyAndID(pkey string) (string, string, error) {
	var (
		key     crypto.PrivKey
		keyData []byte
		err     error
	)
	if pkey == "" {
		key = keypair.New()
		keyData, err = crypto.MarshalPrivateKey(key)
		if err != nil {
			return "", "", fmt.Errorf("marshal private key failed with %w", err)
		}
	} else {
		keyData, err = base64.StdEncoding.DecodeString(pkey)
		if err != nil {
			return "", "", fmt.Errorf("decode private key failed with %w", err)
		}

		key, err = crypto.UnmarshalPrivateKey(keyData)
		if err != nil {
			return "", "", fmt.Errorf("read private key failed with %w", err)
		}
	}

	id, err := peer.IDFromPublicKey(key.GetPublic())
	if err != nil {
		return "", "", fmt.Errorf("id from private key failed with %w", err)
	}

	return id.String(), base64.StdEncoding.EncodeToString(keyData), nil
}

func generateDVKeys(private, public []byte) ([]byte, []byte, error) {
	var (
		priv *ecdsa.PrivateKey
		err  error
	)
	if len(private) > 0 {
		if len(public) > 0 {
			return private, public, nil
		}

		priv, err = jwt.ParseECPrivateKeyFromPEM(private)
		if err != nil {
			return nil, nil, fmt.Errorf("open ecdsa key failed with %w", err)
		}
	} else {
		priv, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, nil, fmt.Errorf("generate ecdsa key failed with %w", err)
		}

		private, err = pemEncodePrivKey(priv)
		if err != nil {
			return nil, nil, err
		}
	}

	public, err = pemEncodePubKey(priv)
	if err != nil {
		return nil, nil, err
	}

	return private, public, nil
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
