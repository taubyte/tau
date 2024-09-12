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
	if err == nil {
		return rk, nil
	}

	k := New()
	err = Save(k, path)
	if err != nil {
		return nil, err
	}

	rk, err = crypto.MarshalPrivateKey(New())
	if err != nil {
		return nil, err
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
	if err == nil {
		err = os.WriteFile(keyPath, data, 0400)
	}

	return err
}

func Load(keyPath string) (crypto.PrivKey, error) {
	_, err := os.Stat(keyPath)
	if err == nil {
		key, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, err
		} else {
			priv, err := crypto.UnmarshalPrivateKey(key)
			if err != nil {
				return nil, err
			} else {
				return priv, nil
			}
		}
	}

	return nil, err
}

// Read key from ENV. key must be encoded in base64
func LoadRawFromEnv() []byte {
	key64, ok := os.LookupEnv("TAUBYTE_KEY")
	if ok {
		key, err := base64.StdEncoding.DecodeString(key64)
		if err == nil {
			return key
		}
	}

	return nil
}

// Read key from ENV. key must be encoded in base64
func LoadRawFromString(key64 string) []byte {
	key, err := base64.StdEncoding.DecodeString(key64)
	if err == nil && key != nil {
		return key
	}

	return nil
}
