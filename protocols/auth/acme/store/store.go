package store

import (
	"context"
	"strings"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/p2p/peer"
	client "github.com/taubyte/p2p/streams/client"
	"github.com/taubyte/p2p/streams/command"
	dirs "github.com/taubyte/utils/fs/dir"
	maps "github.com/taubyte/utils/maps"

	protocolCommon "github.com/taubyte/odo/protocols/common"
)

var (
	MinPeers = 0
	MaxPeers = 4
	logger   = log.Logger("auth.acme.store")
)

// Store implements Store and Cache using taubyte acme service
// NOTE: Must periodically chewck the validity of the certificate by a go-routine. If
//
//	the certififcate is not valid restart the service after a random sleep.
type Store struct {
	node         peer.Node
	client       *client.Client
	cacheDir     dirs.Directory
	errCacheMiss error
}

func New(ctx context.Context, node peer.Node, cacheDir string, errCacheMiss error) (*Store, error) {
	var (
		c   Store
		err error
	)

	logger.Debug("ACME Distributed Store Creation...")
	c.node = node
	c.cacheDir, err = dirs.New(cacheDir)
	if err != nil {
		return nil, err
	}

	c.errCacheMiss = errCacheMiss
	//[]string{"12D3KooWMrLZ2m7dTJf1a1VEsReJnRH1iNRg9U9WyLMQHMZTnjAB", "12D3KooWBm5BkzoAt4yyxodtrRsZUoWZ5aHCg3KRx8WJofAZsPsa"}
	c.client, err = client.New(ctx, node, nil, protocolCommon.AuthProtocol, MinPeers, MaxPeers)
	if err != nil {
		logger.Errorf("ACME Store creation failed: %w", err)
		return nil, err
	}

	logger.Debug("ACME Distributed Store Created!")
	return &c, nil
}

// Get reads a certificate data from the specified file name.
func (d *Store) Get(ctx context.Context, name string) ([]byte, error) {
	logger.Debug("Getting `%s`", name)
	defer logger.Debug("Getting `%s` done", name)

	var (
		body    *command.Body
		dataKey string
	)

	if strings.HasSuffix(name, "+token") || strings.HasSuffix(name, "+rsa") || strings.HasSuffix(name, "+key") || strings.HasSuffix(name, ".key") {
		body = &command.Body{"action": "cache-get", "key": name}
		dataKey = "data"
	} else {
		body = &command.Body{"action": "get", "fqdn": name}
		dataKey = "certificate"
	}

	res, err := d.client.TrySend("acme", *body)
	if err != nil {
		if d.errCacheMiss != nil && err.Error() == d.errCacheMiss.Error() {
			logger.Debugf("Cache miss for `%s` returning ErrCacheMiss", name)
			return nil, d.errCacheMiss
		}
		logger.Errorf("Getting `%s` failed: %w", name, err)
		return nil, err
	}

	pem, err := maps.ByteArray(res, dataKey)
	if err != nil {
		logger.Errorf("Reading PEM error: %w", err)
		return nil, err
	}

	logger.Debugf("Getting `%s` = %v", name, pem)

	return pem, nil
}

// Put writes the certificate data to the specified file name.
// The file will be created with 0600 permissions.
func (d *Store) Put(ctx context.Context, name string, data []byte) error {
	logger.Debugf("Storing `%s`", name)
	defer logger.Debugf("Storing `%s` done", name)

	var body *command.Body

	if strings.HasSuffix(name, "+token") || strings.HasSuffix(name, "+rsa") || strings.HasSuffix(name, "+key") || strings.HasSuffix(name, ".key") {
		body = &command.Body{"action": "cache-set", "key": name, "data": data}
	} else {
		body = &command.Body{"action": "set", "fqdn": name, "certificate": data}
	}

	// write file to DB by sending command
	_, err := d.client.TrySend("acme", *body)
	if err != nil {
		logger.Errorf("Storing `%s` error: %w", name, err)
	}
	return err
}

// Delete removes the specified certificate or tokens (cached data).
func (d *Store) Delete(ctx context.Context, name string) error {
	logger.Debugf("Deleting `%s`", name)
	defer logger.Debugf("Deleting `%s` done", name)
	// client can not delete
	// certificate life cycle is handled by the Auth peers

	// token or any cached data can be deleted
	if strings.HasSuffix(name, "+token") || strings.HasSuffix(name, "+rsa") || strings.HasSuffix(name, "+key") || strings.HasSuffix(name, ".key") {
		_, err := d.client.Send("acme", command.Body{"action": "cache-delete", "key": name})
		if err != nil {
			logger.Error("Deleting `%s` error: %w", name, err)
		}
		return err
	}

	// return a slient nil
	return nil
}
