package store

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/tau/p2p/peer"
	client "github.com/taubyte/tau/p2p/streams/client"
	"github.com/taubyte/tau/p2p/streams/command"
	maps "github.com/taubyte/tau/utils/maps"
	"golang.org/x/crypto/acme/autocert"

	protocolCommon "github.com/taubyte/tau/services/common"
)

var logger = log.Logger("tau.auth.acme.store")
var certFileRegexp = regexp.MustCompile(`(\+token|\+rsa|\+key|\.key)$`)

var (
	// CacheRetryDuration is the maximum time to retry getting cached tokens from kvdb
	CacheRetryDuration = 5 * time.Second
	// CacheRetryInterval is the time between retries for tokens
	CacheRetryInterval = 100 * time.Millisecond

	// CertRetryDuration is the maximum time to wait for another node to obtain a certificate
	CertRetryDuration = 10 * time.Second
	// CertRetryInterval is the time between retries when checking for certificate
	CertRetryInterval = 500 * time.Millisecond
)

type Store struct {
	node     peer.Node
	client   client.SendOnlyClient
	cacheDir autocert.DirCache
	closed   bool
	mu       sync.Mutex
}

func New(ctx context.Context, node peer.Node, cacheDir string) (*Store, error) {
	var (
		c   Store
		err error
	)

	c.node = node
	c.cacheDir = autocert.DirCache(cacheDir)

	logger.Debugf("ACME Distributed Store Creation... (node: %s, cacheDir: %s)", node.ID().ShortString(), cacheDir)

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

	logger.Debugf("Get called with name: %s (isCert: %v)", name, isCert)

	pem, err := d.cacheDir.Get(ctx, name)
	if err == nil {
		return pem, nil
	} else {
		if isCert {
			pem, err = d.cacheDir.Get(ctx, wildcardName)
			if err == nil {
				return pem, nil
			}
		}
	}

	// For tokens/challenges, retry with short interval since kvdb may be slow at scale
	if !isCert {
		deadline := time.Now().Add(CacheRetryDuration)
		for {
			pem, err = d.getDynamicCertificate(name, false)
			if err == nil {
				logger.Debugf("Caching locally `%s`", name)
				d.cacheDir.Put(ctx, name, pem)
				return pem, nil
			}

			if err != autocert.ErrCacheMiss {
				logger.Debugf("Getting `%s` failed: %s", name, err)
				return nil, err
			}

			if time.Now().After(deadline) {
				logger.Debugf("Cache miss for `%s` after retries, returning ErrCacheMiss", name)
				return nil, autocert.ErrCacheMiss
			}

			logger.Debugf("Cache miss for `%s`, retrying...", name)
			time.Sleep(CacheRetryInterval)
		}
	}

	// For certificates, retry with longer interval to allow another node to complete ACME
	deadline := time.Now().Add(CertRetryDuration)
	for {
		// Try dynamic cert (from kvdb)
		pem, err = d.getDynamicCertificate(name, true)
		if err == nil {
			logger.Debugf("Caching locally `%s`", name)
			d.cacheDir.Put(ctx, name, pem)
			return pem, nil
		}

		// Try wildcard dynamic cert
		pem, err = d.getDynamicCertificate(wildcardName, true)
		if err == nil {
			logger.Debugf("Caching locally `%s`", name)
			d.cacheDir.Put(ctx, name, pem)
			return pem, nil
		}

		// Try static cert
		pem, err = d.getStaticCertificate(name)
		if err == nil {
			logger.Debugf("Caching locally `%s`", name)
			d.cacheDir.Put(ctx, name, pem)
			return pem, nil
		}

		// Try wildcard static cert
		pem, err = d.getStaticCertificate(wildcardName)
		if err == nil {
			logger.Debugf("Caching locally `%s`", name)
			d.cacheDir.Put(ctx, name, pem)
			return pem, nil
		}

		if time.Now().After(deadline) {
			logger.Debugf("Not found in acme cache after retries for `%s`, returning ErrCacheMiss", name)
			return nil, autocert.ErrCacheMiss
		}

		logger.Debugf("Cert not found for `%s`, waiting for another node...", name)
		time.Sleep(CertRetryInterval)
	}
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

	if !certFileRegexp.MatchString(name) {
		body = &command.Body{"action": "set", "fqdn": name, "certificate": data}
	} else {
		body = &command.Body{"action": "cache-set", "key": name, "data": data}
	}

	_, err := d.client.Send("acme", *body)
	if err != nil {
		logger.Errorf("Storing `%s` error: %s", name, err.Error())
		return err
	}

	d.cacheDir.Put(ctx, name, data)

	return nil
}

// Delete removes the specified certificate or tokens (cached data).
func (d *Store) Delete(ctx context.Context, name string) error {
	logger.Debugf("Deleting `%s`", name)
	defer logger.Debugf("Deleting `%s` done", name)

	if !certFileRegexp.MatchString(name) {
		defer d.cacheDir.Delete(ctx, name)

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
