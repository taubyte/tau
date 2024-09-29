package fixtures

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"

	"golang.org/x/crypto/ssh"
)

func GenerateSSHKeyPair(bits int) ([]byte, []byte, error) {
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
