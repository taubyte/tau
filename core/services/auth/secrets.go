package auth

import (
	"context"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/p2p/streams/client"
	peerService "github.com/taubyte/tau/p2p/streams/service"
)

type TriggerReason int

const (
	TriggerFirstSecret TriggerReason = iota
	TriggerFirstNode
	TriggerInterval
)

type AuthServiceSecretManager interface {
	GetPublicKeys(ctx context.Context) ([]DistributedKey, error)
	StoreEncrypted(ctx context.Context, secretID string, encryptedKey []byte, encryptedData []byte, nonce []byte, distributedKeyID string) error
	GetEncrypted(ctx context.Context, secretID string) (*EncryptedSecretInfo, error)
	GetDecryptionShares(ctx context.Context, encryptedSecret []byte, distributedKeyID string) ([][]byte, error)
	Delete(ctx context.Context, secretID string) error
	Exists(ctx context.Context, secretID string) (bool, error)
	List(ctx context.Context) ([]string, error)
	CheckAndTriggerDKG(ctx context.Context, trigger TriggerReason) error
	AttachStreams(authService peerService.CommandService)
	Close() error
}

type DistributedKey interface {
	KeyID() string
	PublicKey() []byte
	Members() []peer.ID
	Threshold() int
	MemberCount() int
	CreatedAt() time.Time
}

type PublicKeyOptions struct {
	Limit int
}

type PublicKeyOption func(*PublicKeyOptions)

func Limit(limit int) PublicKeyOption {
	return func(opts *PublicKeyOptions) {
		opts.Limit = limit
	}
}

type EncryptedSecretInfo struct {
	SecretID         string
	EncryptedData    []byte
	Nonce            []byte
	DistributedKeyID string
	Members          []peer.ID
	Threshold        int
	PublicKey        []byte
}

type StreamClient interface {
	New(cmd string, opts ...client.Option[client.Request]) *client.Request
}

type ServiceStreamClient interface {
	New(cmd string, opts ...client.Option[client.Request]) *client.Request
}
