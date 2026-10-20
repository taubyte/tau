package git

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"strings"

	"golang.org/x/crypto/ssh"
)

// ref: https://github.com/keybase/bot-sshca/blob/master/src/keybaseca/sshutils/generate.go#L53
func generateDeployKey() (string, string, error) {
	_privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}

	privateKey, err := x509.MarshalECPrivateKey(_privateKey)
	if err != nil {
		return "", "", err
	}

	privateKeyPEM := &pem.Block{Type: "EC PRIVATE KEY", Bytes: privateKey}
	var private bytes.Buffer
	if err := pem.Encode(&private, privateKeyPEM); err != nil {
		return "", "", err
	}

	pub, err := ssh.NewPublicKey(&_privateKey.PublicKey)
	if err != nil {
		return "", "", err
	}

	return string(ssh.MarshalAuthorizedKey(pub)), private.String(), nil
}

func IsLocalURI(url string) bool {
	return strings.HasPrefix(url, LocalURIScheme)
}
