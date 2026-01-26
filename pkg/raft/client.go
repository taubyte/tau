package raft

import (
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	streamClient "github.com/taubyte/tau/p2p/streams/client"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
)

// Client is a p2p client for communicating with raft cluster nodes
type Client struct {
	*streamClient.Client
	namespace string
}

// NewClient creates a new raft p2p client for the given namespace
func NewClient(node Node, namespace string) (*Client, error) {
	protocol := Protocol(namespace)

	client, err := streamClient.New(node, protocol)
	if err != nil {
		return nil, fmt.Errorf("failed to create stream client: %w", err)
	}

	return &Client{
		Client:    client,
		namespace: namespace,
	}, nil
}

// Set sends a set command to the specified peers (or discovers peers if none specified)
func (c *Client) Set(key string, value []byte, timeout time.Duration, peers ...peer.ID) error {
	body := command.Body{
		keyKey:     key,
		keyValue:   value,
		keyTimeout: float64(timeout.Milliseconds()),
	}

	resp, err := c.Send(cmdSet, body, peers...)
	if err != nil {
		return fmt.Errorf("set failed: %w", err)
	}

	if errMsg, err := resp.Get("error"); err == nil && errMsg != nil {
		return fmt.Errorf("set error: %v", errMsg)
	}

	return nil
}

// Get sends a get command to retrieve a value
func (c *Client) Get(key string, peers ...peer.ID) ([]byte, bool, error) {
	body := command.Body{
		keyKey: key,
	}

	resp, err := c.Send(cmdGet, body, peers...)
	if err != nil {
		return nil, false, fmt.Errorf("get failed: %w", err)
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

// Delete sends a delete command to remove a key
func (c *Client) Delete(key string, timeout time.Duration, peers ...peer.ID) error {
	body := command.Body{
		keyKey:     key,
		keyTimeout: float64(timeout.Milliseconds()),
	}

	resp, err := c.Send(cmdDelete, body, peers...)
	if err != nil {
		return fmt.Errorf("delete failed: %w", err)
	}

	if errMsg, err := resp.Get("error"); err == nil && errMsg != nil {
		return fmt.Errorf("delete error: %v", errMsg)
	}

	return nil
}

// Keys sends a keys command to list keys with a prefix
func (c *Client) Keys(prefix string, peers ...peer.ID) ([]string, error) {
	body := command.Body{
		keyPrefix: prefix,
	}

	resp, err := c.Send(cmdKeys, body, peers...)
	if err != nil {
		return nil, fmt.Errorf("keys failed: %w", err)
	}

	keysVal, err := resp.Get(keyKeys)
	if err != nil || keysVal == nil {
		// No keys or not found - return empty slice
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
		// Unknown type - return empty slice
		return []string{}, nil
	}
}

// Send sends a command to the specified peers
func (c *Client) Send(cmd string, body command.Body, peers ...peer.ID) (cr.Response, error) {
	return c.Client.Send(cmd, body, peers...)
}

// ExchangePeers sends our peer list and receives theirs for convergence
func (c *Client) ExchangePeers(ourStart time.Time, ourPeers map[string]int64, target peer.ID) (time.Time, map[string]int64, error) {
	body := command.Body{
		keyStartTime: ourStart.UnixMilli(),
		keySeenAt:    ourPeers,
	}

	resp, err := c.Client.Send(cmdExchangePeers, body, target)
	if err != nil {
		return time.Time{}, nil, err
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

// toInt64 converts various numeric types to int64
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
