package auth

import (
	"context"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/p2p/streams/client"
	peerService "github.com/taubyte/tau/p2p/streams/service"
)

// TriggerReason indicates why DKG was triggered
type TriggerReason int

const (
	TriggerFirstSecret TriggerReason = iota
	TriggerFirstNode
	TriggerInterval
)

// AuthServiceSecretManager - Interface for auth service (trustless)
// Auth nodes only provide metadata and store encrypted secrets
type AuthServiceSecretManager interface {
	// GetPublicKeys returns list of available distributed keys with metadata
	// Clients use this to select best key for encryption
	GetPublicKeys(ctx context.Context) ([]DistributedKey, error)

	// StoreEncrypted stores an encrypted secret (client encrypts before calling)
	// Auth node never sees the plaintext
	// For hybrid encryption: encryptedKey is Paillier-encrypted symmetric key,
	// encryptedData is AES-encrypted secret, nonce is GCM nonce
	StoreEncrypted(ctx context.Context, secretID string, encryptedKey []byte, encryptedData []byte, nonce []byte, distributedKeyID string) error

	// GetEncrypted returns encrypted data, nonce, and key metadata for decryption
	// Client uses this to get AES-encrypted data and node PIDs for decryption share collection
	// Does NOT return encrypted key - clients request decryption shares by secretID
	GetEncrypted(ctx context.Context, secretID string) (*EncryptedSecretInfo, error)

	// GetDecryptionShares produces decryption shares from this node's key shares
	// Safe to expose: decryption shares alone don't reveal the secret
	// Returns decryption shares for all key shares this node has for the given distributed key
	// Used for single-node case where client can't connect to itself via P2P
	GetDecryptionShares(ctx context.Context, encryptedSecret []byte, distributedKeyID string) ([][]byte, error)

	// Delete removes encrypted secret from KVDB
	Delete(ctx context.Context, secretID string) error

	// Exists checks if a secret exists
	Exists(ctx context.Context, secretID string) (bool, error)

	// List returns all secret IDs
	List(ctx context.Context) ([]string, error)

	// CheckAndTriggerDKG checks if DKG should be triggered and triggers it if needed
	// This is used internally and in tests to manually trigger DKG
	CheckAndTriggerDKG(ctx context.Context, trigger TriggerReason) error

	// AttachStreams attaches the command service and registers all stream handlers
	AttachStreams(authService peerService.CommandService)

	// Close closes the service and cleans up resources
	Close() error
}

// DistributedKey is the interface for distributed key information
// Used by clients to select best key for encryption
type DistributedKey interface {
	// KeyID returns unique identifier for this distributed key
	KeyID() string
	// PublicKey returns the threshold Paillier public key (for encryption)
	PublicKey() []byte
	// Members returns the list of nodes with key shares (PIDs)
	Members() []peer.ID
	// Threshold returns the threshold for decryption
	Threshold() int
	// MemberCount returns the number of nodes
	MemberCount() int
	// CreatedAt returns when the DKG was completed
	CreatedAt() time.Time
}

// PublicKeyOptions contains options for PublicKeys method
type PublicKeyOptions struct {
	Limit int // Maximum number of keys to return (0 = no limit)
}

// PublicKeyOption is a functional option for PublicKeys method
type PublicKeyOption func(*PublicKeyOptions)

// Limit sets the maximum number of keys to return
func Limit(limit int) PublicKeyOption {
	return func(opts *PublicKeyOptions) {
		opts.Limit = limit
	}
}

// EncryptedSecretInfo contains encrypted secret and key metadata
// For hybrid encryption: EncryptedData is AES-encrypted, Nonce is GCM nonce
// Does NOT include encrypted key - clients request decryption shares by secretID
type EncryptedSecretInfo struct {
	SecretID         string    // Secret identifier
	EncryptedData    []byte    // AES-encrypted secret data
	Nonce            []byte    // GCM nonce for AES decryption
	DistributedKeyID string    // Key ID used for encryption
	Members          []peer.ID // List of nodes with key shares (PIDs)
	Threshold        int       // Threshold for decryption
	PublicKey        []byte    // Public key for verification
}

// StreamClient interface for client-to-service P2P communication
// Used by clients to communicate with auth services for CRUD operations
// Commands: get-public-keys, store-encrypted, get-encrypted, get-decryption-shares, secret-delete, secret-exists, secret-list
type StreamClient interface {
	New(cmd string, opts ...client.Option[client.Request]) *client.Request
}

// ServiceStreamClient interface for service-to-service P2P communication
// Used by auth services to communicate with other auth services for crypto operations
// Commands: dkg-init, dkg-continue, dkg-finalize
type ServiceStreamClient interface {
	New(cmd string, opts ...client.Option[client.Request]) *client.Request
}
