package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"

	libp2pCrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
)

const (
	atRestInfo = "tau/secrets/share-at-rest/v2"

	// AtRestKeySize is the length of a generated at-rest key, in bytes.
	AtRestKeySize = 32
)

// GenerateAtRestKey makes a key an operator can configure as Config.Key.
func GenerateAtRestKey() (string, error) {
	key := make([]byte, AtRestKeySize)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("generating an at-rest key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(key), nil
}

// atRest seals this node's shares before they touch the disk.
//
// A share is the one piece of key material a node cannot re-derive, so a stolen
// disk must not hand it over. The key encrypting it therefore has to come from
// somewhere the disk does not hold.
type atRest struct {
	aead cipher.AEAD

	// configured records whether the key was configured or derived from the
	// node's own identity, so the posture can be stated at start rather than
	// assumed.
	configured bool
}

// newAtRest derives the share-encryption key. Configured material is preferred:
// it lives outside the data directory, so taking the disk yields nothing. The
// node's private key is the fallback -- weaker, because it sits in the same
// repository the shares do, but still far better than plaintext, and it keeps
// clusters that have not configured a key working rather than failing to boot.
func newAtRest(node host.Host, configuredKey string) (*atRest, error) {
	material, configured, err := keyMaterial(node, configuredKey)
	if err != nil {
		return nil, err
	}

	key, err := hkdf.Key(sha256.New, material, []byte(node.ID().String()), atRestInfo, 32)
	if err != nil {
		return nil, fmt.Errorf("deriving the share-encryption key: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("share-encryption cipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("share-encryption aead: %w", err)
	}

	return &atRest{aead: aead, configured: configured}, nil
}

func keyMaterial(node host.Host, configuredKey string) ([]byte, bool, error) {
	if configuredKey != "" {
		return []byte(configuredKey), true, nil
	}

	priv := node.Peerstore().PrivKey(node.ID())
	if priv == nil {
		return nil, false, errors.New("node has no private key to protect shares with")
	}

	raw, err := libp2pCrypto.MarshalPrivateKey(priv)
	if err != nil {
		return nil, false, fmt.Errorf("marshaling node private key: %w", err)
	}

	return raw, false, nil
}

// posture describes what is protecting the shares, for the operator to see.
func (a *atRest) posture() string {
	if a.configured {
		return "shares are encrypted with the configured at-rest key"
	}

	return "shares are encrypted with node-local material only: no at-rest key is configured, so taking this node's data directory is enough to read its share"
}

func (a *atRest) seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, a.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("share nonce: %w", err)
	}

	return a.aead.Seal(nonce, nonce, plaintext, nil), nil
}

func (a *atRest) open(sealed []byte) ([]byte, error) {
	if len(sealed) < a.aead.NonceSize() {
		return nil, errors.New("stored share is too short")
	}

	nonce, body := sealed[:a.aead.NonceSize()], sealed[a.aead.NonceSize():]

	plaintext, err := a.aead.Open(nil, nonce, body, nil)
	if err != nil {
		return nil, fmt.Errorf("opening the stored share: %w", err)
	}

	return plaintext, nil
}
