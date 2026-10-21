package config

import (
	"encoding/base64"
	"fmt"
	"slices"

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
