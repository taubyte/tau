package secrets

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudflare/circl/group"

	secretsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2"
	"github.com/taubyte/tau/pkg/secrets/tdh"
)

// Sealed seals a secret once, for every node's Init to write identically.
//
// The same bundle goes to every node, so every node writes the same record
// under the same CID: one version, however many nodes hold it.
func Sealed(ref Ref, value []byte, writtenAt time.Time) (*secretsv2.InitSecret, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}

	dek, ciphertext, err := tdh.Seal(value, contentAAD(ref))
	if err != nil {
		return nil, fmt.Errorf("encrypting secret %s: %w", ref, err)
	}

	return &secretsv2.InitSecret{
		Ref:        wireRef(ref),
		Cid:        contentID(ciphertext),
		Ciphertext: ciphertext,
		Dek:        dek,
		WrittenAt:  writtenAt.UnixNano(),
	}, nil
}

// Init writes a sealed bundle into this node's store before the node serves.
//
// The data key is wrapped under the newest key this node holds. A node that
// holds none forms its own one-member key, which is what makes Init work on a
// fresh store with no peer to run a round with. That key is an ordinary older
// epoch the moment the cluster rotates to its real roster: every secret is
// carried forward by its own member, and the one-member key's wrappings and
// share are retired.
func (srv *Service) Init(ctx context.Context, bundle *secretsv2.InitBundle) error {
	key, err := srv.ActiveKey(ctx)
	if err != nil {
		epoch, err := srv.keys.Epoch(ctx)
		if err != nil {
			return err
		}
		if err := srv.generateKey(ctx, epoch, []string{srv.self()}); err != nil {
			return err
		}
		if key, err = srv.ActiveKey(ctx); err != nil {
			return err
		}
	}

	public, err := key.PublicKey()
	if err != nil {
		return err
	}

	for _, secret := range bundle.GetSecrets() {
		if err := srv.initOne(ctx, key, public, secret); err != nil {
			return err
		}
	}
	return nil
}

// Unsealed opens a sealed secret with the data key it carries.
func Unsealed(secret *secretsv2.InitSecret) (Ref, []byte, error) {
	ref, err := refOf(secret.GetRef())
	if err != nil {
		return Ref{}, nil, err
	}

	value, err := tdh.Unseal(secret.GetDek(), secret.GetCiphertext(), contentAAD(ref))
	if err != nil {
		return Ref{}, nil, fmt.Errorf("secret %s: the data key does not open the ciphertext: %w", ref, err)
	}
	return ref, value, nil
}

func (srv *Service) initOne(ctx context.Context, key *KeyMeta, public group.Element, secret *secretsv2.InitSecret) error {
	ref, _, err := Unsealed(secret)
	if err != nil {
		return err
	}

	v := version{Ref: ref, CID: secret.GetCid()}
	if err := srv.records.Put(ctx, v, secret.GetCiphertext(), secret.GetWrittenAt()); err != nil {
		return err
	}

	wrapping, err := tdh.Wrap(public, secret.GetDek(), wrapAAD(v, key.KeyID))
	if err != nil {
		return fmt.Errorf("wrapping the data key for %s: %w", ref, err)
	}
	return srv.wrappings.Put(ctx, v, key.KeyID, srv.self(), wrapping)
}
