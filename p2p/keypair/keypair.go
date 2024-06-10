package keypair

import (
	"encoding/base64"
	"os"

	crypto "github.com/libp2p/go-libp2p/core/crypto"
)

func New() crypto.PrivKey {
	priv, _, err := crypto.GenerateKeyPair(crypto.Ed25519, 1)
	if err != nil {
		return nil
	}
	return priv
}

func NewPersistant(path string) ([]byte, error) {
	rk, err := LoadRaw(path)
	if err != nil {
		k := New()
		if err = Save(k, path); err != nil {
			return nil, err
		}

		if rk, err = crypto.MarshalPrivateKey(New()); err != nil {
			return nil, err
		}
	}

	return rk, nil
}

func NewRaw() []byte {
	data, _ := crypto.MarshalPrivateKey(New())
	return data
}

func LoadRaw(keyPath string) ([]byte, error) {
	if _, err := os.Stat(keyPath); err != nil {
		return nil, err
	}

	return os.ReadFile(keyPath)
}

func Save(priv crypto.PrivKey, keyPath string) error {
	data, err := crypto.MarshalPrivateKey(priv)
	if err != nil {
		return err
	}

	return os.WriteFile(keyPath, data, 0400)
}

func Load(keyPath string) (crypto.PrivKey, error) {
	if _, err := os.Stat(keyPath); err != nil {
		return nil, err
	}

	key, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}

	return crypto.UnmarshalPrivateKey(key)
}

// Read key from ENV. key must be encoded in base64
func LoadRawFromEnv() []byte {
	if key64, ok := os.LookupEnv("TAUBYTE_KEY"); ok {
		if key, err := base64.StdEncoding.DecodeString(key64); err == nil {
			return key
		}
	}

	return nil
}

// Read key from ENV. key must be encoded in base64
func LoadRawFromString(key64 string) []byte {
	if key, err := base64.StdEncoding.DecodeString(key64); err == nil {
		return key
	}

	return nil
}
