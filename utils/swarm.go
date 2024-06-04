package utils

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/libp2p/go-libp2p/core/pnet"
)

var expectedKeyLength = 6

func GenerateSwarmKey() (string, error) {
	key := make([]byte, 32)
	_, err := rand.Read(key)
	if err != nil {
		return "", fmt.Errorf("rand read failed with: %w", err)
	}

	return "/key/swarm/psk/1.0.0//base16/" + hex.EncodeToString(key), nil
}

// Note: This will generate a predictable key
// Only use if you know whjat you're doing
func GenerateSwarmKeyFromString(data string) string {
	// Hash the data using SHA-256
	hash := sha256.Sum256([]byte(data))

	// Return the swarm key in the specified format
	return "/key/swarm/psk/1.0.0/base16/" + hex.EncodeToString(hash[:])
}

func FormatSwarmKey(key string) (pnet.PSK, error) {
	_key := strings.Split(key, "/")
	_key = deleteEmpty(_key)

	if len(_key) != expectedKeyLength {
		return nil, errors.New("swarm key is not correctly formatted")
	}

	format := fmt.Sprintf(`/%s/%s/%s/%s/
/%s/
%s`, _key[0], _key[1], _key[2], _key[3], _key[4], _key[5])

	return []byte(format), nil
}

func deleteEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}

	r := make([]string, 0, len(s))
	for _, str := range s {
		if str != "" {
			r = append(r, str)
		}
	}
	return r
}
