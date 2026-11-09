package storage

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/ipfs/go-cid"

	storagev2 "github.com/taubyte/tau/pkg/proto/gen/tau/storage/v2"
)

// Possession is proven, never claimed. A probe hands a holder a fresh nonce; the
// holder descends the root's DAG along paths the nonce chooses and answers an HMAC,
// keyed by the nonce, over every block it read on the way. Which blocks is unknown
// until the nonce is, so only a node holding them can answer, and the challenger,
// which holds them too, computes the same answer to compare. Each probe reads a few
// blocks rather than the object; fresh nonces cover the rest across passes.

const (
	// probePaths is how many root-to-leaf descents one probe reads.
	probePaths = 2

	// nonceSize is the nonce a challenger sends. A holder refuses one shorter
	// than minNonce or longer than maxNonce: a short one could be answered from
	// a table, and a long one is work for nothing.
	nonceSize = 32
	minNonce  = 16
	maxNonce  = 64

	// probeWait bounds one probe: a holder that stalls costs its own call, never
	// the pass that asked it.
	probeWait = 10 * time.Second
)

// ErrNotHeld is a block this node does not have, asked of it without fetching.
var ErrNotHeld = errors.New("storage: this node does not hold that block")

// possession is the answer to a probe of root with nonce, over this node's own
// blocks: ErrNotHeld when one on the nonce's paths is not here.
func possession(ctx context.Context, blocks Blocks, root cid.Cid, nonce []byte) ([]byte, error) {
	mac := hmac.New(sha256.New, nonce)
	for path := range probePaths {
		c := root
		for depth := 0; ; depth++ {
			raw, err := blocks.Block(ctx, c)
			if err != nil {
				return nil, err
			}
			mac.Write(c.Bytes())
			mac.Write(raw)
			links, err := blocks.Links(ctx, c)
			if err != nil {
				return nil, err
			}
			if len(links) == 0 {
				break
			}
			c = links[descent(nonce, path, depth, len(links))]
		}
	}
	return mac.Sum(nil), nil
}

// descent is which of n links a path takes at depth, as the nonce decides.
func descent(nonce []byte, path, depth, n int) int {
	h := sha256.New()
	h.Write(nonce)
	var at [16]byte
	binary.BigEndian.PutUint64(at[:8], uint64(path))
	binary.BigEndian.PutUint64(at[8:], uint64(depth))
	h.Write(at[:])
	return int(binary.BigEndian.Uint64(h.Sum(nil)[:8]) % uint64(n))
}

// prove asks holder to prove it holds root. A holder that answers it does not, or
// answers wrong, is false with no error; one that cannot be asked is an error,
// which proves nothing either way. ErrNotHeld is this node not holding root
// itself, so it has nothing to check an answer against.
func (s *server) prove(ctx context.Context, holder string, root cid.Cid) (bool, error) {
	nonce := make([]byte, nonceSize)
	rand.Read(nonce)
	want, err := possession(ctx, s.bytes, root, nonce)
	if err != nil {
		return false, err
	}
	svc, err := s.peer(holder)
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(ctx, probeWait)
	defer cancel()
	res, err := svc.Probe(ctx, connect.NewRequest(&storagev2.ProbeRequest{RootCid: root.String(), Nonce: nonce}))
	switch {
	case connect.CodeOf(err) == connect.CodeNotFound:
		return false, nil
	case err != nil:
		return false, err
	}
	return hmac.Equal(res.Msg.GetProof(), want), nil
}

// answerProbe is this node's answer to a probe of root with nonce.
func (s *server) answerProbe(ctx context.Context, root cid.Cid, nonce []byte) ([]byte, error) {
	if len(nonce) < minNonce || len(nonce) > maxNonce {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("storage: a probe nonce is %d to %d bytes, not %d", minNonce, maxNonce, len(nonce)))
	}
	proof, err := possession(ctx, s.bytes, root, nonce)
	if errors.Is(err, ErrNotHeld) {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return proof, err
}
