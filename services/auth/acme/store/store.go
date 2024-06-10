package store

import (
	"context"
	"strings"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/tau/p2p/peer"
	client "github.com/taubyte/tau/p2p/streams/client"
	"github.com/taubyte/tau/p2p/streams/command"
	dirs "github.com/taubyte/utils/fs/dir"
	maps "github.com/taubyte/utils/maps"

	protocolCommon "github.com/taubyte/tau/services/common"
)

var logger = log.Logger("tau.auth.acme.store")

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
	c.client, err = client.New(node, protocolCommon.AuthProtocol)
	if err != nil {
		logger.Error("ACME Store creation failed:", err.Error())
		return nil, err
	}

	logger.Debug("ACME Distributed Store Created!")
	return &c, nil
}

// Get reads a certificate data from the specified file name.
func (d *Store) Get(ctx context.Context, name string) ([]byte, error) {
	logger.Debugf("Getting `%s`", name)
	defer logger.Debugf("Getting `%s` done", name)

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

	res, err := d.client.Send("acme", *body)
	if err != nil {
		if d.errCacheMiss != nil && err.Error() == d.errCacheMiss.Error() {
			logger.Debugf("Cache miss for `%s` returning ErrCacheMiss", name)
			return nil, d.errCacheMiss
		}
		logger.Errorf("Getting `%s` failed: %s", name, err.Error())
		return nil, err
	}

	pem, err := maps.ByteArray(res, dataKey)
	if err != nil {
		logger.Error("Reading PEM failed with:", err.Error())
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
	_, err := d.client.Send("acme", *body)
	if err != nil {
		logger.Errorf("Storing `%s` error: %s", name, err.Error())
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
			logger.Errorf("Deleting `%s` error: %s", name, err.Error())
		}
		return err
	}

	return nil
}
