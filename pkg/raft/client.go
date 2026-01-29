package raft

import (
	"crypto/cipher"
	"fmt"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	taupeer "github.com/taubyte/tau/p2p/peer"
	streamClient "github.com/taubyte/tau/p2p/streams/client"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
)

// Client represents a Raft p2p client for external use
type Client interface {
	// Set stores a key-value pair
	Set(key string, value []byte, timeout time.Duration, peers ...peer.ID) error
	// Get retrieves a value by key
	// barrierNs is the barrier timeout in nanoseconds. If > 0, ensures consistency before reading.
	// Must be > 0 and <= MaxGetHandlerBarrierTimeout, otherwise returns ErrInvalidBarrier.
	Get(key string, barrierNs int64, peers ...peer.ID) ([]byte, bool, error)
	// Delete removes a key
	Delete(key string, timeout time.Duration, peers ...peer.ID) error
	// Keys returns all keys matching a prefix
	Keys(prefix string, peers ...peer.ID) ([]string, error)
	// Close closes the client
	Close() error
}

// internalClient represents a Raft p2p client for internal cluster operations
type internalClient interface {
	// JoinVoter requests to join as a voter
	JoinVoter(peerID peer.ID, timeout time.Duration, peers ...peer.ID) error
	// ExchangePeers exchanges peer discovery information
	ExchangePeers(ourStart time.Time, ourPeers map[string]int64, target peer.ID) (time.Time, map[string]int64, error)
	// Close closes the client
	Close() error
}

type client struct {
	*streamClient.Client
	encryptionCipher cipher.AEAD
}

// newInternalClient creates a new internal raft p2p client for the given namespace
func newInternalClient(node taupeer.Node, namespace string, encryptionCipher cipher.AEAD) (internalClient, error) {
	protocol := Protocol(namespace)

	streamCli, err := streamClient.New(node, protocol)
	if err != nil {
		return nil, fmt.Errorf("failed to create stream client: %w", err)
	}

	return &client{
		Client:           streamCli,
		encryptionCipher: encryptionCipher,
	}, nil
}

// NewClient creates a new raft p2p client for the given namespace
func NewClient(node taupeer.Node, namespace string, encryptionCipher cipher.AEAD) (Client, error) {
	cli, err := newInternalClient(node, namespace, encryptionCipher)
	if err != nil {
		return nil, err
	}
	return cli.(*client), nil
}

func (c *client) Set(key string, value []byte, timeout time.Duration, peers ...peer.ID) error {
	body := command.Body{
		keyKey:     key,
		keyValue:   value,
		keyTimeout: float64(timeout.Milliseconds()),
	}

	if c.encryptionCipher != nil {
		encryptedBody, err := encryptBody(body, c.encryptionCipher)
		if err != nil {
			return fmt.Errorf("encrypting body: %w", err)
		}
		body = encryptedBody
	}

	resp, err := c.sendCommand(cmdSet, body, peers...)
	if err != nil {
		return fmt.Errorf("set failed: %w", err)
	}

	if c.encryptionCipher != nil {
		decryptedResp, err := decryptResponse(resp, c.encryptionCipher)
		if err != nil {
			return fmt.Errorf("decrypting response: %w", err)
		}
		resp = decryptedResp
	}

	return nil
}

func (c *client) Get(key string, barrierNs int64, peers ...peer.ID) ([]byte, bool, error) {
	if barrierNs != 0 {
		if barrierNs <= 0 {
			return nil, false, ErrInvalidBarrier
		}
		barrierTimeout := time.Duration(barrierNs) * time.Nanosecond
		if barrierTimeout > MaxGetHandlerBarrierTimeout {
			return nil, false, ErrInvalidBarrier
		}
	}

	body := command.Body{
		keyKey: key,
	}

	if barrierNs > 0 {
		body[keyBarrier] = barrierNs
	}

	if c.encryptionCipher != nil {
		encryptedBody, err := encryptBody(body, c.encryptionCipher)
		if err != nil {
			return nil, false, fmt.Errorf("encrypting body: %w", err)
		}
		body = encryptedBody
	}

	resp, err := c.sendCommand(cmdGet, body, peers...)
	if err != nil {
		return nil, false, fmt.Errorf("get failed: %w", err)
	}

	if c.encryptionCipher != nil {
		decryptedResp, err := decryptResponse(resp, c.encryptionCipher)
		if err != nil {
			return nil, false, fmt.Errorf("decrypting response: %w", err)
		}
		resp = decryptedResp
	}

	found, err := resp.Get(keyFound)
	if err != nil {
		return nil, false, nil
	}
	foundBool, _ := found.(bool)

	if !foundBool {
		return nil, false, nil
	}

	val, err := resp.Get(keyValue)
	if err != nil {
		return nil, false, nil
	}

	switch v := val.(type) {
	case []byte:
		return v, true, nil
	case string:
		return []byte(v), true, nil
	default:
		return nil, false, fmt.Errorf("unexpected value type: %T", val)
	}
}

func (c *client) Delete(key string, timeout time.Duration, peers ...peer.ID) error {
	body := command.Body{
		keyKey:     key,
		keyTimeout: float64(timeout.Milliseconds()),
	}

	if c.encryptionCipher != nil {
		encryptedBody, err := encryptBody(body, c.encryptionCipher)
		if err != nil {
			return fmt.Errorf("encrypting body: %w", err)
		}
		body = encryptedBody
	}

	resp, err := c.sendCommand(cmdDelete, body, peers...)
	if err != nil {
		return fmt.Errorf("delete failed: %w", err)
	}

	if c.encryptionCipher != nil {
		decryptedResp, err := decryptResponse(resp, c.encryptionCipher)
		if err != nil {
			return fmt.Errorf("decrypting response: %w", err)
		}
		resp = decryptedResp
	}

	return nil
}

func (c *client) JoinVoter(peerID peer.ID, timeout time.Duration, peers ...peer.ID) error {
	body := command.Body{
		keyPeer:    peerID.String(),
		keyTimeout: float64(timeout.Milliseconds()),
	}

	if c.encryptionCipher != nil {
		encryptedBody, err := encryptBody(body, c.encryptionCipher)
		if err != nil {
			return fmt.Errorf("encrypting body: %w", err)
		}
		body = encryptedBody
	}

	resCh, err := c.Client.New(cmdJoinVoter, streamClient.Body(body), streamClient.To(peers...), streamClient.Threshold(1)).Do()
	if err != nil {
		return fmt.Errorf("join voter failed: %w", err)
	}

	var firstErr error
	sawNoLeader := false
	for res := range resCh {
		if res == nil {
			continue
		}
		if err := res.Error(); err != nil {
			if strings.Contains(err.Error(), ErrNoLeader.Error()) {
				sawNoLeader = true
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("join voter failed: %w", err)
			}
			res.Close()
			continue
		}
		resp := res.Response
		res.Close()

		if c.encryptionCipher != nil {
			decryptedResp, err := decryptResponse(resp, c.encryptionCipher)
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("decrypting response: %w", err)
				}
				continue
			}
			resp = decryptedResp
		}

		return nil
	}

	if sawNoLeader {
		return ErrNoLeader
	}
	if firstErr != nil {
		return firstErr
	}
	return fmt.Errorf("join voter failed: no responses")
}

func (c *client) Keys(prefix string, peers ...peer.ID) ([]string, error) {
	body := command.Body{
		keyPrefix: prefix,
	}

	if c.encryptionCipher != nil {
		encryptedBody, err := encryptBody(body, c.encryptionCipher)
		if err != nil {
			return nil, fmt.Errorf("encrypting body: %w", err)
		}
		body = encryptedBody
	}

	resp, err := c.sendCommand(cmdKeys, body, peers...)
	if err != nil {
		return nil, fmt.Errorf("keys failed: %w", err)
	}

	if c.encryptionCipher != nil {
		respBody := make(command.Body)
		for k, v := range resp {
			respBody[k] = v
		}
		decryptedBody, err := decryptBody(respBody, c.encryptionCipher)
		if err != nil {
			return nil, fmt.Errorf("decrypting response: %w", err)
		}
		resp = make(cr.Response)
		for k, v := range decryptedBody {
			resp[k] = v
		}
	}

	keysVal, err := resp.Get(keyKeys)
	if err != nil || keysVal == nil {
		return []string{}, nil
	}

	switch v := keysVal.(type) {
	case []string:
		return v, nil
	case []interface{}:
		keys := make([]string, 0, len(v))
		for _, k := range v {
			if s, ok := k.(string); ok {
				keys = append(keys, s)
			}
		}
		return keys, nil
	default:
		return []string{}, nil
	}
}

func (c *client) ExchangePeers(ourStart time.Time, ourPeers map[string]int64, target peer.ID) (time.Time, map[string]int64, error) {
	body := command.Body{
		keyStartTime: ourStart.UnixMilli(),
		keySeenAt:    ourPeers,
	}

	if c.encryptionCipher != nil {
		encryptedBody, err := encryptBody(body, c.encryptionCipher)
		if err != nil {
			return time.Time{}, nil, fmt.Errorf("encrypting body: %w", err)
		}
		body = encryptedBody
	}

	resCh, err := c.Client.New(cmdExchangePeers,
		streamClient.Body(body),
		streamClient.To(target),
		streamClient.Threshold(1),
	).Do()
	if err != nil {
		return time.Time{}, nil, err
	}

	for res := range resCh {
		if res == nil {
			continue
		}
		if err := res.Error(); err != nil {
			res.Close()
			return time.Time{}, nil, err
		}
		resp := res.Response
		res.Close()

		if c.encryptionCipher != nil {
			decryptedResp, err := decryptResponse(resp, c.encryptionCipher)
			if err != nil {
				return time.Time{}, nil, fmt.Errorf("decrypting response: %w", err)
			}
			resp = decryptedResp
		}

		theirStartRaw, _ := resp.Get(keyStartTime)
		theirStart := time.UnixMilli(toInt64(theirStartRaw))

		theirPeersRaw, _ := resp.Get(keySeenAt)
		theirPeers := make(map[string]int64)

		if m, ok := theirPeersRaw.(map[string]interface{}); ok {
			for k, v := range m {
				theirPeers[k] = toInt64(v)
			}
		}

		return theirStart, theirPeers, nil
	}

	return time.Time{}, nil, fmt.Errorf("exchange peers failed: no responses")
}

// sendCommand sends a command and returns the first successful response.
func (c *client) sendCommand(cmd string, body command.Body, peers ...peer.ID) (cr.Response, error) {
	opts := []streamClient.Option[streamClient.Request]{
		streamClient.Body(body),
	}
	if len(peers) > 0 {
		opts = append(opts, streamClient.To(peers...), streamClient.Threshold(len(peers)))
	}

	resCh, err := c.Client.New(cmd, opts...).Do()
	if err != nil {
		return nil, fmt.Errorf("sending command %q failed: %w", cmd, err)
	}

	var firstErr error
	for res := range resCh {
		if res == nil {
			continue
		}
		if err := res.Error(); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("command %q failed: %w", cmd, err)
			}
			res.Close()
			continue
		}
		resp := res.Response
		res.Close()
		return resp, nil
	}

	if firstErr != nil {
		return nil, firstErr
	}
	return nil, fmt.Errorf("command %q failed: no responses", cmd)
}

func (c *client) Close() error {
	c.Client.Close()
	return nil
}

func toInt64(v interface{}) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case uint64:
		return int64(n)
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case uint:
		return int64(n)
	case int32:
		return int64(n)
	case uint32:
		return int64(n)
	default:
		return 0
	}
}
