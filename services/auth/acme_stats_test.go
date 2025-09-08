package auth

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/taubyte/tau/config"
	"github.com/taubyte/tau/p2p/keypair"
	"github.com/taubyte/tau/p2p/streams/command"
	"github.com/taubyte/tau/pkg/kvdb/mock"

	"gotest.tools/v3/assert"
)

// Test ACME service handler with comprehensive test data sequences
func TestAcmeServiceHandlerWithFixtures(t *testing.T) {
	ctx := context.Background()
	mockFactory := mock.New()
	cfg := &config.Node{
		P2PListen:   []string{"/ip4/0.0.0.0/tcp/12374"},
		P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12374"},
		PrivateKey:  keypair.NewRaw(),
		Databases:   mockFactory,
		Root:        t.TempDir(),
	}
	svc, err := New(ctx, cfg)
	assert.NilError(t, err)
	defer svc.Close()

	// Test sequence: setup data then test ACME handler actions
	// 1. Set up test certificates first
	certData := []byte("test-cert-data")
	err = svc.setACMECertificate(ctx, "handler.example.com", certData)
	assert.NilError(t, err)

	staticCertData := []byte("static-cert-data")
	err = svc.setACMEStaticCertificate(ctx, "static.example.com", staticCertData)
	assert.NilError(t, err)

	cacheData := []byte("cache-data")
	err = svc.setACMECache(ctx, "cache-key", cacheData)
	assert.NilError(t, err)

	// 2. Now test ACME handler actions with existing data
	// Test get action
	getResp, err := svc.acmeServiceHandler(ctx, nil, command.Body{"action": "get", "fqdn": "handler.example.com"})
	assert.NilError(t, err)
	assert.Assert(t, getResp != nil)

	// Test get-static action
	getStaticResp, err := svc.acmeServiceHandler(ctx, nil, command.Body{"action": "get-static", "fqdn": "static.example.com"})
	assert.NilError(t, err)
	assert.Assert(t, getStaticResp != nil)

	// Test set action (creates new data)
	setResp, err := svc.acmeServiceHandler(ctx, nil, command.Body{"action": "set", "fqdn": "new.example.com", "certificate": []byte("new-cert")})
	assert.NilError(t, err)
	assert.Assert(t, setResp == nil) // set action returns nil response

	// Test set-static action (creates new data)
	setStaticResp, err := svc.acmeServiceHandler(ctx, nil, command.Body{"action": "set-static", "fqdn": "new-static.example.com", "certificate": []byte("new-static-cert")})
	assert.NilError(t, err)
	assert.Assert(t, setStaticResp == nil) // set-static action returns nil response

	// Test cache-get action
	cacheGetResp, err := svc.acmeServiceHandler(ctx, nil, command.Body{"action": "cache-get", "key": "cache-key"})
	assert.NilError(t, err)
	assert.Assert(t, cacheGetResp != nil)

	// Test cache-set action (creates new cache data)
	cacheSetResp, err := svc.acmeServiceHandler(ctx, nil, command.Body{"action": "cache-set", "key": "new-cache-key", "data": []byte("new-cache-data")})
	assert.NilError(t, err)
	assert.Assert(t, cacheSetResp == nil) // cache-set action returns nil response

	// Test cache-delete action
	cacheDeleteResp, err := svc.acmeServiceHandler(ctx, nil, command.Body{"action": "cache-delete", "key": "cache-key"})
	assert.NilError(t, err)
	assert.Assert(t, cacheDeleteResp == nil) // cache-delete action returns nil response

	// Test invalid action
	_, err = svc.acmeServiceHandler(ctx, nil, command.Body{"action": "invalid"})
	assert.Assert(t, err != nil, "Expected error for invalid action")
}

// Test P2P stream endpoints with comprehensive test data sequences
func TestP2PStreamEndpointsWithFixtures(t *testing.T) {
	ctx := context.Background()
	mockFactory := mock.New()
	cfg := &config.Node{
		P2PListen:   []string{"/ip4/0.0.0.0/tcp/12373"},
		P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12373"},
		PrivateKey:  keypair.NewRaw(),
		Databases:   mockFactory,
		Root:        t.TempDir(),
	}
	svc, err := New(ctx, cfg)
	assert.NilError(t, err)
	defer svc.Close()

	// Test sequence: setup stream routes then test endpoints
	svc.setupStreamRoutes()

	// Test ACME certificate workflow: set -> get -> cache -> delete
	// 1. Set certificate
	certData := []byte("test-certificate-data")
	err = svc.setACMECertificate(ctx, "test.example.com", certData)
	assert.NilError(t, err)

	// 2. Get certificate (should work now)
	retrievedCert, err := svc.getACMECertificate(ctx, "test.example.com")
	assert.NilError(t, err)
	assert.DeepEqual(t, retrievedCert, certData)

	// 3. Set cache data
	cacheData := []byte("test-cache-data")
	err = svc.setACMECache(ctx, "test-cache-key", cacheData)
	assert.NilError(t, err)

	// 4. Get cache data (should work now)
	retrievedCache, err := svc.getACMECache(ctx, "test-cache-key")
	assert.NilError(t, err)
	assert.DeepEqual(t, retrievedCache, cacheData)

	// 5. Delete cache data
	err = svc.deleteACMECache(ctx, "test-cache-key")
	assert.NilError(t, err)

	// 6. Verify deletion
	_, err = svc.getACMECache(ctx, "test-cache-key")
	assert.Assert(t, err != nil, "Expected error after deletion")

	// Test static certificate workflow
	staticCertData := []byte("static-certificate-data")
	err = svc.setACMEStaticCertificate(ctx, "static.example.com", staticCertData)
	assert.NilError(t, err)

	retrievedStaticCert, err := svc.getACMEStaticCertificate(ctx, "static.example.com")
	assert.NilError(t, err)
	assert.DeepEqual(t, retrievedStaticCert, staticCertData)

	// Test wildcard certificate fallback
	wildcardCertData := []byte("wildcard-cert-data")
	err = svc.setACMEStaticCertificate(ctx, "*.example.com", wildcardCertData)
	assert.NilError(t, err)

	// Try to get a specific subdomain - should fall back to wildcard
	specificCert, err := svc.getACMEStaticCertificate(ctx, "sub.example.com")
	assert.NilError(t, err)
	assert.DeepEqual(t, specificCert, wildcardCertData)
}

// Test statsServiceHandler with different input validation scenarios
func TestStatsServiceHandlerInputValidation(t *testing.T) {
	ctx := context.Background()
	mockFactory := mock.New()
	cfg := &config.Node{
		P2PListen:   []string{"/ip4/0.0.0.0/tcp/12371"},
		P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12371"},
		PrivateKey:  keypair.NewRaw(),
		Databases:   mockFactory,
		Root:        t.TempDir(),
	}
	svc, err := New(ctx, cfg)
	assert.NilError(t, err)
	defer svc.Close()

	// Test with missing action
	_, err = svc.statsServiceHandler(ctx, nil, command.Body{})
	assert.Assert(t, err != nil, "Expected error for missing action")

	// Test with invalid action type
	_, err = svc.statsServiceHandler(ctx, nil, command.Body{"action": 123})
	assert.Assert(t, err != nil, "Expected error for invalid action type")

	// Test with valid db action
	dbResp, err := svc.statsServiceHandler(ctx, nil, command.Body{"action": "db"})
	assert.NilError(t, err)
	assert.Assert(t, dbResp != nil)

	// Test with unsupported action
	_, err = svc.statsServiceHandler(ctx, nil, command.Body{"action": "unsupported"})
	assert.Assert(t, err != nil, "Expected error for unsupported action")
}

// Test stats service handler
func TestStatsServiceHandler(t *testing.T) {
	ctx := context.Background()
	mockFactory := mock.New()
	cfg := &config.Node{
		P2PListen:   []string{"/ip4/0.0.0.0/tcp/12356"},
		P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12356"},
		PrivateKey:  keypair.NewRaw(),
		Databases:   mockFactory,
		Root:        t.TempDir(),
	}
	svc, err := New(ctx, cfg)
	assert.NilError(t, err)
	defer svc.Close()

	// Test stats handler with db action
	statsResp, err := svc.statsServiceHandler(ctx, nil, command.Body{"action": "db"})
	assert.NilError(t, err)
	assert.Assert(t, statsResp != nil)

	// Test stats handler with invalid action
	_, err = svc.statsServiceHandler(ctx, nil, command.Body{"action": "invalid"})
	assert.Assert(t, err != nil, "Expected error for invalid stats action")
}

// Test stats service handler with different actions
func TestStatsServiceHandlerActions(t *testing.T) {
	ctx := context.Background()
	mockFactory := mock.New()
	cfg := &config.Node{
		P2PListen:   []string{"/ip4/0.0.0.0/tcp/12366"},
		P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12366"},
		PrivateKey:  keypair.NewRaw(),
		Databases:   mockFactory,
		Root:        t.TempDir(),
	}
	svc, err := New(ctx, cfg)
	assert.NilError(t, err)
	defer svc.Close()

	// Test stats handler with different actions to cover more code paths
	// Test with empty action
	_, err = svc.statsServiceHandler(ctx, nil, command.Body{})
	assert.Assert(t, err != nil, "Expected error for missing action")

	// Test with invalid action type
	_, err = svc.statsServiceHandler(ctx, nil, command.Body{"action": 123})
	assert.Assert(t, err != nil, "Expected error for invalid action type")

	// Test with valid db action
	dbResp, err := svc.statsServiceHandler(ctx, nil, command.Body{"action": "db"})
	assert.NilError(t, err)
	assert.Assert(t, dbResp != nil)
}

// Test ACME static certificate with wildcard fallback
func TestACMEStaticCertificateWildcardFallback(t *testing.T) {
	ctx := context.Background()
	mockFactory := mock.New()
	cfg := &config.Node{
		P2PListen:   []string{"/ip4/0.0.0.0/tcp/12367"},
		P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12367"},
		PrivateKey:  keypair.NewRaw(),
		Databases:   mockFactory,
		Root:        t.TempDir(),
	}
	svc, err := New(ctx, cfg)
	assert.NilError(t, err)
	defer svc.Close()

	// Test getACMEStaticCertificate with wildcard fallback logic
	// First, set a wildcard certificate
	wildcardDomain := "*.example.com"
	wildcardCert := []byte("wildcard-cert-data")
	err = svc.setACMEStaticCertificate(ctx, wildcardDomain, wildcardCert)
	assert.NilError(t, err)

	// Now try to get a specific subdomain - it should fall back to wildcard
	specificDomain := "sub.example.com"
	retrievedCert, err := svc.getACMEStaticCertificate(ctx, specificDomain)
	assert.NilError(t, err)
	assert.DeepEqual(t, retrievedCert, wildcardCert)

	// Test with a domain that doesn't match the wildcard pattern
	nonMatchingDomain := "other.com"
	_, err = svc.getACMEStaticCertificate(ctx, nonMatchingDomain)
	assert.Assert(t, err != nil, "Expected error for non-matching domain")
}

// Test ACME certificate functions
func TestACMECertificateFunctions(t *testing.T) {
	ctx := context.Background()
	mockFactory := mock.New()
	cfg := &config.Node{
		P2PListen:   []string{"/ip4/0.0.0.0/tcp/12357"},
		P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12357"},
		PrivateKey:  keypair.NewRaw(),
		Databases:   mockFactory,
		Root:        t.TempDir(),
	}
	svc, err := New(ctx, cfg)
	assert.NilError(t, err)
	defer svc.Close()

	// Test setACMECertificate
	certData := []byte("test-certificate-data")
	err = svc.setACMECertificate(ctx, "example.com", certData)
	assert.NilError(t, err)

	// Test getACMECertificate
	retrievedCert, err := svc.getACMECertificate(ctx, "example.com")
	assert.NilError(t, err)
	assert.DeepEqual(t, retrievedCert, certData)

	// Test setACMEStaticCertificate
	staticCertData := []byte("static-certificate-data")
	err = svc.setACMEStaticCertificate(ctx, "static.example.com", staticCertData)
	assert.NilError(t, err)

	// Test getACMEStaticCertificate
	retrievedStaticCert, err := svc.getACMEStaticCertificate(ctx, "static.example.com")
	assert.NilError(t, err)
	assert.DeepEqual(t, retrievedStaticCert, staticCertData)

	// Test getACMECache
	cacheData := []byte("cache-data")
	err = svc.setACMECache(ctx, "test-key", cacheData)
	assert.NilError(t, err)

	retrievedCache, err := svc.getACMECache(ctx, "test-key")
	assert.NilError(t, err)
	assert.DeepEqual(t, retrievedCache, cacheData)

	// Test deleteACMECache
	err = svc.deleteACMECache(ctx, "test-key")
	assert.NilError(t, err)

	// Verify deletion
	_, err = svc.getACMECache(ctx, "test-key")
	assert.Assert(t, err != nil, "Expected error after deletion")

	// TODO: Fix ACME service handler tests
	// The acmeServiceHandler tests are failing due to certificate cache miss issues
	// This needs investigation of the exact data flow and path structure
}

// Test ACME cache error paths
func TestACMECacheErrorPaths(t *testing.T) {
	ctx := context.Background()
	mockFactory := mock.New()
	cfg := &config.Node{
		P2PListen:   []string{"/ip4/0.0.0.0/tcp/12387"},
		P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12387"},
		PrivateKey:  keypair.NewRaw(),
		Databases:   mockFactory,
		Root:        t.TempDir(),
	}
	svc, err := New(ctx, cfg)
	assert.NilError(t, err)
	defer svc.Close()

	// Test getACMECache with non-existent key (should return ErrCacheMiss)
	data, err := svc.getACMECache(ctx, "non-existent-key")
	assert.Assert(t, err != nil, "Expected error for non-existent key")
	assert.Assert(t, data == nil, "Expected nil data for non-existent key")

	// Test getACMECache with empty data (should trigger cleanup and return ErrCacheMiss)
	// First set some empty data
	key := "test-empty-key"
	keyBase := "/acme/cache/" + base64.StdEncoding.EncodeToString([]byte(key))
	err = svc.db.Put(ctx, keyBase+"/data", nil)
	assert.NilError(t, err)

	// Now test getACMECache - it should detect empty data, clean up, and return error
	data, err = svc.getACMECache(ctx, key)
	assert.Assert(t, err != nil, "Expected error for empty data")
	assert.Assert(t, data == nil, "Expected nil data for empty data")

	// Verify cleanup happened
	_, err = svc.db.Get(ctx, keyBase+"/data")
	assert.Assert(t, err != nil, "Expected data to be cleaned up")
}

// Test ACME certificate error paths
func TestACMECertificateErrorPaths(t *testing.T) {
	ctx := context.Background()
	mockFactory := mock.New()
	cfg := &config.Node{
		P2PListen:   []string{"/ip4/0.0.0.0/tcp/12388"},
		P2PAnnounce: []string{"/ip4/127.0.0.1/tcp/12388"},
		PrivateKey:  keypair.NewRaw(),
		Databases:   mockFactory,
		Root:        t.TempDir(),
	}
	svc, err := New(ctx, cfg)
	assert.NilError(t, err)
	defer svc.Close()

	// Test getACMECertificate with non-existent domain (should fall back to static certificate)
	cert, err := svc.getACMECertificate(ctx, "non-existent-domain.com")
	// This should fail both ACME and static certificate lookups
	assert.Assert(t, err != nil, "Expected error for non-existent domain")
	assert.Assert(t, cert == nil, "Expected nil certificate for non-existent domain")

	// Test getACMECertificate with empty certificate (should trigger cleanup)
	domain := "test-empty-cert.com"
	key := "/acme/" + base64.StdEncoding.EncodeToString([]byte(domain)) + "/certificate/pem"
	err = svc.db.Put(ctx, key, nil) // Set empty certificate
	assert.NilError(t, err)

	// Now test getACMECertificate - it should detect empty certificate, clean up, and return error
	cert, err = svc.getACMECertificate(ctx, domain)
	assert.Assert(t, err != nil, "Expected error for empty certificate")
	assert.Assert(t, cert == nil, "Expected nil certificate for empty certificate")

	// Verify cleanup happened
	_, err = svc.db.Get(ctx, key)
	assert.Assert(t, err != nil, "Expected certificate to be cleaned up")
}
