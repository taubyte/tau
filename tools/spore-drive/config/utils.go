package config

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"slices"

	jwt "github.com/dgrijalva/jwt-go"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/p2p/keypair"
)

func appendNew[T comparable](slice0 []T, slice1 ...T) []T {
	result := make([]T, len(slice0), len(slice0)+len(slice1))
	copy(result, slice0)

	for _, item := range slice1 {
		if !slices.Contains(result, item) {
			result = append(result, item)
		}
	}

	return result
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
