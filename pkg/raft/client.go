package raft

import (
	"crypto/cipher"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	streamClient "github.com/taubyte/tau/p2p/streams/client"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
)

type Client struct {
	*streamClient.Client
	encryptionCipher cipher.AEAD
}

// NewClient creates a new raft p2p client for the given namespace
func NewClient(node Node, namespace string, encryptionCipher cipher.AEAD) (*Client, error) {
	protocol := Protocol(namespace)

	client, err := streamClient.New(node, protocol)
	if err != nil {
		return nil, fmt.Errorf("failed to create stream client: %w", err)
	}

	return &Client{
		Client:           client,
		encryptionCipher: encryptionCipher,
	}, nil
}

func (c *Client) Set(key string, value []byte, timeout time.Duration, peers ...peer.ID) error {
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

	resp, err := c.Send(cmdSet, body, peers...)
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

	if errMsg, err := resp.Get("error"); err == nil && errMsg != nil {
		return fmt.Errorf("set error: %v", errMsg)
	}

	return nil
}

func (c *Client) Get(key string, peers ...peer.ID) ([]byte, bool, error) {
	body := command.Body{
		keyKey: key,
	}

	if c.encryptionCipher != nil {
		encryptedBody, err := encryptBody(body, c.encryptionCipher)
		if err != nil {
			return nil, false, fmt.Errorf("encrypting body: %w", err)
		}
		body = encryptedBody
	}

	resp, err := c.Send(cmdGet, body, peers...)
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

func (c *Client) Delete(key string, timeout time.Duration, peers ...peer.ID) error {
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

	resp, err := c.Send(cmdDelete, body, peers...)
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

	if errMsg, err := resp.Get("error"); err == nil && errMsg != nil {
		return fmt.Errorf("delete error: %v", errMsg)
	}

	return nil
}

func (c *Client) JoinVoter(peerID peer.ID, timeout time.Duration, peers ...peer.ID) error {
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

	resp, err := c.Send(cmdJoinVoter, body, peers...)
	if err != nil {
		return fmt.Errorf("join voter failed: %w", err)
	}

	if c.encryptionCipher != nil {
		decryptedResp, err := decryptResponse(resp, c.encryptionCipher)
		if err != nil {
			return fmt.Errorf("decrypting response: %w", err)
		}
		resp = decryptedResp
	}

	if errMsg, err := resp.Get("error"); err == nil && errMsg != nil {
		return fmt.Errorf("join voter error: %v", errMsg)
	}

	return nil
}

func (c *Client) Keys(prefix string, peers ...peer.ID) ([]string, error) {
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

	resp, err := c.Send(cmdKeys, body, peers...)
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

func (c *Client) Send(cmd string, body command.Body, peers ...peer.ID) (cr.Response, error) {
	return c.Client.Send(cmd, body, peers...)
}

func (c *Client) ExchangePeers(ourStart time.Time, ourPeers map[string]int64, target peer.ID) (time.Time, map[string]int64, error) {
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

	resp, err := c.Client.Send(cmdExchangePeers, body, target)
	if err != nil {
		return time.Time{}, nil, err
	}

	if c.encryptionCipher != nil {
		respBody := make(command.Body)
		for k, v := range resp {
			respBody[k] = v
		}
		decryptedBody, err := decryptBody(respBody, c.encryptionCipher)
		if err != nil {
			return time.Time{}, nil, fmt.Errorf("decrypting response: %w", err)
		}
		resp = make(cr.Response)
		for k, v := range decryptedBody {
			resp[k] = v
		}
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
