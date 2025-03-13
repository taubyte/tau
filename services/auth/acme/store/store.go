package store

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/tau/p2p/peer"
	client "github.com/taubyte/tau/p2p/streams/client"
	"github.com/taubyte/tau/p2p/streams/command"
	maps "github.com/taubyte/utils/maps"
	"golang.org/x/crypto/acme/autocert"

	protocolCommon "github.com/taubyte/tau/services/common"
)

var logger = log.Logger("tau.auth.acme.store")
var certFileRegexp = regexp.MustCompile(`(\+token|\+rsa|\+key|\.key)$`)

type Store struct {
	node     peer.Node
	client   *client.Client
	cacheDir autocert.DirCache
	closed   bool
	mu       sync.Mutex
}

func New(ctx context.Context, node peer.Node, cacheDir string) (*Store, error) {
	var (
		c   Store
		err error
	)

	logger.Debug("ACME Distributed Store Creation...")
	c.node = node
	c.cacheDir = autocert.DirCache(cacheDir)

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
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil, errors.New("store is closed")
	}
	d.mu.Unlock()

	isCert := !certFileRegexp.MatchString(name)
	wildcardName := "*." + strings.Join(strings.Split(name, ".")[1:], ".")

	// check local cache
	pem, err := d.cacheDir.Get(ctx, name)
	if err == nil {
		return pem, nil
	} else {
		// try wildcard
		if isCert {
			pem, err = d.cacheDir.Get(ctx, wildcardName)
			if err == nil {
				return pem, nil
			}
		}
	}

	// check remote cache
	pem, err = d.getDynamicCertificate(name, isCert)
	if err != nil {
		if !isCert {
			if err.Error() == autocert.ErrCacheMiss.Error() {
				logger.Debugf("Cache miss for `%s` returning ErrCacheMiss", name)
				return nil, autocert.ErrCacheMiss
			}
			logger.Debugf("Getting `%s` failed: %w", name, err)
			return nil, err
		}

		// try wildcard
		pem, err = d.getDynamicCertificate(wildcardName, true)
		if err != nil {
			// Try static
			pem, err = d.getStaticCertificate(name)
			if err != nil {
				// check if wildcard is set
				pem, err = d.getStaticCertificate(wildcardName)
				if err != nil {
					logger.Debugf("Not found in acme cache... trying to get a static certificate for `%s` failed: %w", name, err)
					return nil, autocert.ErrCacheMiss
				}
			}
		}
	}

	// cache locally
	d.cacheDir.Put(ctx, name, pem)

	return pem, nil
}

func (d *Store) getDynamicCertificate(name string, isCert bool) ([]byte, error) {
	var (
		body    *command.Body
		dataKey string
	)

	if isCert {
		body = &command.Body{"action": "get", "fqdn": name}
		dataKey = "certificate"
	} else {
		body = &command.Body{"action": "cache-get", "key": name}
		dataKey = "data"
	}

	res, err := d.client.Send("acme", *body)
	if err != nil {
		return nil, err
	}

	pem, err := maps.ByteArray(res, dataKey)
	if err != nil {
		logger.Error("Reading PEM failed with:", err.Error())
		return nil, err
	}

	return pem, nil
}

func (d *Store) getStaticCertificate(name string) ([]byte, error) {
	var err error

	resp, err := d.client.Send("acme", command.Body{"action": "get-static", "fqdn": name})
	if err != nil {
		return nil, fmt.Errorf("failed get certificate for %s with %v", name, err)
	}

	certData, err := maps.ByteArray(resp, "certificate")
	if err != nil {
		return nil, fmt.Errorf("failed finding certificate with %v", err)
	}

	return certData, nil
}

// Put writes the certificate data to the specified file name.
// The file will be created with 0600 permissions.
func (d *Store) Put(ctx context.Context, name string, data []byte) error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return errors.New("store is closed")
	}
	d.mu.Unlock()

	logger.Debugf("Storing `%s`", name)
	defer logger.Debugf("Storing `%s` done", name)

	var body *command.Body

	if certFileRegexp.MatchString(name) {
		body = &command.Body{"action": "set", "fqdn": name, "certificate": data}
	} else {
		body = &command.Body{"action": "cache-set", "key": name, "data": data}
	}

	// write file to DB by sending command
	_, err := d.client.Send("acme", *body)
	if err != nil {
		logger.Errorf("Storing `%s` error: %s", name, err.Error())
		return err
	}

	// cache locally
	d.cacheDir.Put(ctx, name, data)

	return nil
}

// Delete removes the specified certificate or tokens (cached data).
func (d *Store) Delete(ctx context.Context, name string) error {
	logger.Debugf("Deleting `%s`", name)
	defer logger.Debugf("Deleting `%s` done", name)

	// token or any cached data can be deleted
	if !certFileRegexp.MatchString(name) {
		// (deferred) delete locally
		defer d.cacheDir.Delete(ctx, name)

		// delete remotely
		_, err := d.client.Send("acme", command.Body{"action": "cache-delete", "key": name})
		if err != nil {
			logger.Errorf("Deleting `%s` error: %s", name, err.Error())
			return err
		}
	}

	return nil
}

func (d *Store) Close() error {
	d.mu.Lock()
	d.closed = true
	d.mu.Unlock()
	return nil
}
