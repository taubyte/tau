package secrets

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/cloudflare/circl/group"
	"github.com/libp2p/go-libp2p/core/host"
	peerCore "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/p2p/rpc"
	secretsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2/secretsv2connect"
	"github.com/taubyte/tau/pkg/secrets/tdh"
)

// Client reads and writes secrets on any node, whether or not it runs the
// service: it encrypts before a write leaves it, and opens a read itself from a
// threshold of partial decryptions. No member it talks to sees a plaintext.
type Client struct {
	host    host.Host
	members func() []peerCore.ID
	local   func() *Service
	conns   peers
}

// ErrNoMembers is a client that can find no node running the service.
var ErrNoMembers = errors.New("secrets: no secrets service is reachable")

// settleWindow is how long a read keeps trying a secret that exists but
// cannot be opened yet: a wrapping written on one member takes a moment to
// replicate to the others whose partials the read needs.
const settleWindow = 15 * time.Second

// NewClient builds a client from h. members is who runs the service, asked on
// every call; local is this node's own service, or nil, and is used instead of
// a stream whenever a call is for this node.
func NewClient(h host.Host, members func() []peerCore.ID, local func() *Service) (*Client, error) {
	if h == nil {
		return nil, ErrNoHost
	}
	if members == nil {
		members = func() []peerCore.ID { return nil }
	}
	if local == nil {
		local = func() *Service { return nil }
	}
	return &Client{host: h, members: members, local: local, conns: peers{host: h, open: map[peerCore.ID]*rpc.Client{}}}, nil
}

// Close releases the streams this client opened.
func (c *Client) Close() error {
	c.conns.close()
	return nil
}

// targets are the members to ask, this node's own first when it runs one.
func (c *Client) targets() []peerCore.ID {
	var out []peerCore.ID
	if c.local() != nil {
		out = append(out, c.host.ID())
	}
	for _, p := range c.members() {
		if p != c.host.ID() || c.local() != nil {
			if !containsPeer(out, p) {
				out = append(out, p)
			}
		}
	}
	return out
}

func containsPeer(ps []peerCore.ID, p peerCore.ID) bool {
	for _, have := range ps {
		if have == p {
			return true
		}
	}
	return false
}

// at is the service on one member: this node's own handler, or a stream.
func (c *Client) at(to peerCore.ID) (secretsv2connect.SecretsServiceClient, error) {
	if to == c.host.ID() {
		if srv := c.local(); srv != nil {
			return &secretsHandler{srv}, nil
		}
		return nil, ErrNoMembers
	}
	conn, err := c.conns.get(to)
	if err != nil {
		return nil, err
	}
	return secretsv2connect.NewSecretsServiceClient(conn.HTTP(), conn.URL()), nil
}

// first runs call against members in order until one answers.
func (c *Client) first(call func(api secretsv2connect.SecretsServiceClient) error) error {
	targets := c.targets()
	if len(targets) == 0 {
		return ErrNoMembers
	}

	var failures []error
	for _, to := range targets {
		api, err := c.at(to)
		if err == nil {
			err = call(api)
		}
		if err == nil {
			return nil
		}
		// An answer about the request is the answer: another member would
		// give the same one.
		if code := connect.CodeOf(err); code == connect.CodeInvalidArgument || code == connect.CodeNotFound {
			return unwire(err)
		}
		failures = append(failures, fmt.Errorf("%s: %w", to, err))
	}
	return fmt.Errorf("%w: %w", ErrUnavailable, errors.Join(failures...))
}

// Set encrypts value under the cluster's active key and stores it.
func (c *Client) Set(ctx context.Context, ref Ref, value []byte) error {
	if err := ref.Validate(); err != nil {
		return err
	}

	return c.first(func(api secretsv2connect.SecretsServiceClient) error {
		resp, err := api.Key(ctx, connect.NewRequest(&secretsv2.KeyRequest{Form: true}))
		if err != nil {
			return err
		}
		key, err := groupKey(resp.Msg)
		if err != nil {
			return err
		}

		dek, ciphertext, err := tdh.Seal(value, contentAAD(ref))
		if err != nil {
			return fmt.Errorf("encrypting secret %s: %w", ref, err)
		}
		wrapping, err := tdh.Wrap(key.public, dek, wrapAAD(version{Ref: ref, CID: contentID(ciphertext)}, key.id))
		if err != nil {
			return fmt.Errorf("wrapping the data key for %s: %w", ref, err)
		}

		_, err = api.Put(ctx, connect.NewRequest(&secretsv2.PutRequest{
			Ref:        wireRef(ref),
			KeyId:      key.id,
			Ciphertext: ciphertext,
			Wrapping:   &secretsv2.Wrapping{R: wrapping.R, WrappedDek: wrapping.WrappedDEK},
		}))
		return err
	})
}

// Secret is the value a run-time variable refers to.
func (c *Client) Secret(ctx context.Context, project, environment, secretID string) (string, error) {
	value, err := c.Get(ctx, Ref{Scope: Project(project, environment), Name: secretID})
	if err != nil {
		return "", err
	}
	return string(value), nil
}

// Get opens a stored secret here, from a threshold of partial decryptions.
//
// A member that does not hold the record may only be behind, so every member
// is asked before a secret is called absent. One that exists and cannot be
// opened yet is retried until settleWindow or the caller's deadline.
func (c *Client) Get(ctx context.Context, ref Ref) ([]byte, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(settleWindow)
	if own, ok := ctx.Deadline(); ok && own.Before(deadline) {
		deadline = own
	}

	for {
		plaintext, err := c.tryGet(ctx, ref)
		switch {
		case err == nil:
			return plaintext, nil
		case errors.Is(err, ErrNotSet), errors.Is(err, ErrNoMembers), errors.Is(err, ErrInvalidRef):
			return nil, err
		case !time.Now().Before(deadline):
			return nil, fmt.Errorf("secret %s: %w: %w", ref, ErrUnavailable, err)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (c *Client) tryGet(ctx context.Context, ref Ref) ([]byte, error) {
	plaintext, _, err := c.tryOpen(ctx, ref, secretsv2connect.SecretsServiceClient.Get)
	return plaintext, err
}

// Previous opens the version the newest one replaced, and says when it was
// replaced. It asks each member once and does not wait for one to settle: a
// previous version is a fallback, and its absence is the common answer.
func (c *Client) Previous(ctx context.Context, ref Ref) ([]byte, time.Time, error) {
	if err := ref.Validate(); err != nil {
		return nil, time.Time{}, err
	}
	plaintext, sealed, err := c.tryOpen(ctx, ref, secretsv2connect.SecretsServiceClient.Previous)
	if err != nil {
		return nil, time.Time{}, err
	}
	return plaintext, time.Unix(0, sealed.GetReplacedAt()), nil
}

type fetchSealed func(api secretsv2connect.SecretsServiceClient, ctx context.Context, req *connect.Request[secretsv2.Ref]) (*connect.Response[secretsv2.Sealed], error)

func (c *Client) tryOpen(ctx context.Context, ref Ref, fetch fetchSealed) ([]byte, *secretsv2.Sealed, error) {
	targets := c.targets()
	if len(targets) == 0 {
		return nil, nil, ErrNoMembers
	}

	var failures []error
	for _, to := range targets {
		plaintext, sealed, err := c.openAt(ctx, to, ref, fetch)
		if err == nil {
			return plaintext, sealed, nil
		}
		if errors.Is(err, errMiss) {
			continue
		}
		failures = append(failures, fmt.Errorf("%s: %w", to, err))
	}
	if len(failures) == 0 {
		return nil, nil, fmt.Errorf("secret %s: %w", ref, ErrNotSet)
	}
	return nil, nil, errors.Join(failures...)
}

var errMiss = errors.New("this member holds no such secret")

// openAt reads the sealed secret from one member and opens it.
func (c *Client) openAt(ctx context.Context, to peerCore.ID, ref Ref, fetch fetchSealed) ([]byte, *secretsv2.Sealed, error) {
	api, err := c.at(to)
	if err != nil {
		return nil, nil, err
	}

	got, err := fetch(api, ctx, connect.NewRequest(wireRef(ref)))
	if err != nil {
		return nil, nil, err
	}
	sealed := got.Msg
	if !sealed.GetFound() {
		return nil, nil, errMiss
	}

	resp, err := api.Key(ctx, connect.NewRequest(&secretsv2.KeyRequest{KeyId: sealed.GetKeyId()}))
	if err != nil {
		return nil, nil, err
	}
	key, err := groupKey(resp.Msg)
	if err != nil {
		return nil, nil, err
	}

	v := version{Ref: ref, CID: sealed.GetCid()}
	if v.CID != contentID(sealed.GetCiphertext()) {
		return nil, nil, fmt.Errorf("secret %s: the version served does not name its ciphertext", ref)
	}
	wrapping := &tdh.Wrapping{R: sealed.GetWrapping().GetR(), WrappedDEK: sealed.GetWrapping().GetWrappedDek()}
	partials, err := c.partials(ctx, v, key, sealed.GetWriter())
	if err != nil {
		return nil, nil, err
	}

	dek, err := tdh.Unwrap(wrapping, key.public, partials, key.threshold, wrapAAD(v, key.id))
	if err != nil {
		return nil, nil, fmt.Errorf("unwrapping the data key for %s: %w", ref, err)
	}

	plaintext, err := tdh.Unseal(dek, sealed.GetCiphertext(), contentAAD(ref))
	if err != nil {
		return nil, nil, fmt.Errorf("opening secret %s: %w", ref, err)
	}
	return plaintext, sealed, nil
}

// partials collects a threshold of partial decryptions against one writer's
// wrapping, from the key's own members.
func (c *Client) partials(ctx context.Context, v version, key *groupKeyOf, writer string) ([]*tdh.Partial, error) {
	partials := make([]*tdh.Partial, 0, key.threshold)
	var failures []error

	for _, member := range key.members {
		if uint(len(partials)) == key.threshold {
			break
		}

		api, err := c.at(member)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", member, err))
			continue
		}
		resp, err := api.Partial(ctx, connect.NewRequest(&secretsv2.PartialRequest{Ref: wireRef(v.Ref), Cid: v.CID, KeyId: key.id, Writer: writer}))
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", member, err))
			continue
		}
		partials = append(partials, &tdh.Partial{ID: resp.Msg.GetId(), D: resp.Msg.GetD()})
	}

	if uint(len(partials)) < key.threshold {
		return nil, fmt.Errorf("collected %d of %d partial decryptions for %s: %w",
			len(partials), key.threshold, v, errors.Join(failures...))
	}
	return partials, nil
}

// Delete removes a secret.
func (c *Client) Delete(ctx context.Context, ref Ref) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	return c.first(func(api secretsv2connect.SecretsServiceClient) error {
		_, err := api.Delete(ctx, connect.NewRequest(wireRef(ref)))
		return err
	})
}

// List names the secrets of one scope, sorted.
func (c *Client) List(ctx context.Context, scope Scope) ([]string, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	var names []string
	err := c.first(func(api secretsv2connect.SecretsServiceClient) error {
		resp, err := api.List(ctx, connect.NewRequest(wireScope(scope)))
		if err != nil {
			return err
		}
		names = resp.Msg.GetNames()
		return nil
	})
	return names, err
}

// groupKeyOf is a GroupKey a client can use.
type groupKeyOf struct {
	id        string
	public    group.Element
	threshold uint
	members   []peerCore.ID
}

func groupKey(k *secretsv2.GroupKey) (*groupKeyOf, error) {
	public := tdh.Group.NewElement()
	if err := public.UnmarshalBinary(k.GetPublic()); err != nil {
		return nil, fmt.Errorf("deserializing group public key: %w", err)
	}

	members := make([]peerCore.ID, 0, len(k.GetMembers()))
	for _, name := range k.GetMembers() {
		pid, err := peerCore.Decode(name)
		if err != nil {
			return nil, fmt.Errorf("decoding member %s: %w", name, err)
		}
		members = append(members, pid)
	}

	threshold := uint(k.GetThreshold())
	if threshold == 0 || int(threshold) > len(members) {
		return nil, fmt.Errorf("key %s reports threshold %d over %d members", k.GetKeyId(), threshold, len(members))
	}

	return &groupKeyOf{id: k.GetKeyId(), public: public, threshold: threshold, members: members}, nil
}
