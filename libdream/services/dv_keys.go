package services

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
)

// TODO: find a spot for this same thing in spore-drive
func generateDVKeys() ([]byte, []byte, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate ecdsa key failed with: %s", err)
	}
	privateKey, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal private key failed with: %s", err)
	}
	privateKeyPEM := &pem.Block{Type: "EC PRIVATE KEY", Bytes: privateKey}
	var private bytes.Buffer
	err = pem.Encode(&private, privateKeyPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("pem encode private key failed with:  %s", err)
	}
	publicKey, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal public key failed with: %s", err)
	}
	publicKeyPEM := &pem.Block{Type: "PUBLIC KEY", Bytes: publicKey}
	var public bytes.Buffer
	err = pem.Encode(&public, publicKeyPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("failed encoding public key with: %s", err)
	}
	return private.Bytes(), public.Bytes(), nil
}
