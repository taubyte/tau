package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/spore-drive/config"
	"golang.org/x/crypto/ssh"
)

func generateSSHKeyPair(bits int) ([]byte, []byte, error) {
	priv, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(priv),
	})
	if privPEM == nil {
		return nil, nil, errors.New("failed to encode private key to PEM format")
	}

	pub, err := ssh.NewPublicKey(&priv.PublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate SSH public key: %w", err)
	}
	pubSSH := ssh.MarshalAuthorizedKey(pub)

	return privPEM, pubSSH, nil
}

func main() {
	p, err := config.New(afero.NewBasePathFs(afero.NewOsFs(), "fixtures"), "/config")
	if err != nil {
		panic(err)
	}

	err = p.Cloud().Domain().SetRoot("test.com")
	if err != nil {
		panic(err)
	}

	err = p.Cloud().Domain().SetGenerated("gtest.com")
	if err != nil {
		panic(err)
	}

	err = p.Cloud().Domain().Validation().Generate()
	if err != nil {
		panic(err)
	}

	err = p.Cloud().P2P().Swarm().Generate()
	if err != nil {
		panic(err)
	}

	err = p.Auth().Add("main").SetUsername("tau")
	if err != nil {
		panic(err)
	}

	err = p.Auth().Add("main").SetPassword("testtest")
	if err != nil {
		panic(err)
	}

	err = p.Auth().Add("withkey").SetUsername("tau")
	if err != nil {
		panic(err)
	}

	err = p.Auth().Add("withkey").SetKey("keys/test.pem")
	if err != nil {
		panic(err)
	}

	privKeyData, _, err := generateSSHKeyPair(256)
	if err != nil {
		panic(err)
	}

	privKeyFile, err := p.Auth().Add("withkey").Create()
	if err != nil {
		panic(err)
	}
	defer privKeyFile.Close()

	_, err = io.Copy(privKeyFile, bytes.NewBuffer(privKeyData))
	if err != nil {
		panic(err)
	}

	err = p.Shapes().Shape("shape1").Services().Set("auth", "seer")
	if err != nil {
		panic(err)
	}

	err = p.Shapes().Shape("shape1").Ports().Set("main", 4242)
	if err != nil {
		panic(err)
	}

	err = p.Shapes().Shape("shape1").Ports().Set("lite", 4262)
	if err != nil {
		panic(err)
	}

	err = p.Shapes().Shape("shape2").Services().Set("gateway", "patrick", "monkey")
	if err != nil {
		panic(err)
	}

	err = p.Shapes().Shape("shape2").Ports().Set("main", 6242)
	if err != nil {
		panic(err)
	}

	err = p.Shapes().Shape("shape2").Ports().Set("lite", 6262)
	if err != nil {
		panic(err)
	}

	err = p.Shapes().Shape("shape2").Plugins().Set("plugin1@v0.1")
	if err != nil {
		panic(err)
	}

	host1 := p.Hosts().Host("host1")
	err = host1.Addresses().Add("1.2.3.4/24")
	if err != nil {
		panic(err)
	}

	err = host1.Addresses().Add("4.3.2.1/24")
	if err != nil {
		panic(err)
	}

	err = host1.SSH().SetFullAddress("1.2.3.4:4242")
	if err != nil {
		panic(err)
	}

	err = host1.SSH().Auth().Add("main")
	if err != nil {
		panic(err)
	}

	err = host1.SetLocation(1.25, 25.1)
	if err != nil {
		panic(err)
	}

	err = host1.Shapes().Instance("shape1").SetKey("CAESQIWC2KRhsEexLpN4DsJwki4S56IN5IreCANf89+F+OpTWn7Tf+RwZnUbiZYdxsTFrbBJQ9S+A0oFp8a1SSAN2EE=")
	if err != nil {
		panic(err)
	}

	err = host1.Shapes().Instance("shape2").SetKey("CAESQHLGyFbnI2GP7e3Gib9ut7IFDxrkbTbs7LFAJYhe0w0LXEtYrH7HyODglOFY3oXQ+kCfoFcvqvZnAD6K5UavO2c=")
	if err != nil {
		panic(err)
	}

	host2 := p.Hosts().Host("host2")
	err = host2.Addresses().Add("8.2.3.4/24")
	if err != nil {
		panic(err)
	}

	err = host2.Addresses().Add("4.3.2.8/24")
	if err != nil {
		panic(err)
	}

	err = host2.SSH().SetFullAddress("8.2.3.4:4242")
	if err != nil {
		panic(err)
	}

	err = host2.SSH().Auth().Add("main")
	if err != nil {
		panic(err)
	}

	err = host2.SetLocation(1.25, 25.1)
	if err != nil {
		panic(err)
	}

	err = host2.Shapes().Instance("shape1").SetKey("CAESQDpF3eQuEbGsjSRkf3uE6E4SV3dvwSSMUcNJkimOUc0hO6gPoZjsq/NO/FwVz8FoZ4LG/5DSF2B/Rl+vJCNLlUI=")
	if err != nil {
		panic(err)
	}

	err = host2.Shapes().Instance("shape2").SetKey("CAESQIA03gtBTeL8eYNQKcJ+VqKLgarHfofd5I/CV/zEsxHiqfihV9ZXjl0qtaTPEWExBgqRn+w2YLD6FQy8zBdEabI=")
	if err != nil {
		panic(err)
	}

	err = p.Cloud().P2P().Bootstrap().Shape("shape1").Append("host2", "host1")
	if err != nil {
		panic(err)
	}

	err = p.Cloud().P2P().Bootstrap().Shape("shape2").Append("host2", "host1")
	if err != nil {
		panic(err)
	}

	p.Sync()
}
