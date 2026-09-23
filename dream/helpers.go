package dream

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	mrand "math/rand"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/multiformats/go-multiaddr"
	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/p2p/peer"
	httpIface "github.com/taubyte/tau/pkg/http"
)

// boundP2PPort is the tcp port a node is listening on.
//
// THE BIND IS THE ALLOCATION. Every node dream starts listens on port 0, so
// the number does not exist until the host is up — this is where it is read,
// and nothing in dream may predict it. Reserving a port by binding a throwaway
// listener and closing it again, which is what this replaced, leaves a window
// in which any other process can take the same number; four dreaming packages
// running in parallel lost that race often enough to fail a universe outright.
//
// IT READS THE LISTEN ADDRESSES, NOT THE ANNOUNCED ONES. Announcing is a
// configurable view of a node and can be overridden; listening is what it did.
func boundP2PPort(node peer.Node) (int, error) {
	for _, addr := range node.Peer().Network().ListenAddresses() {
		value, err := addr.ValueForProtocol(multiaddr.P_TCP)
		if err != nil {
			continue
		}
		port, err := strconv.Atoi(value)
		if err == nil && port != 0 {
			return port, nil
		}
	}

	return 0, fmt.Errorf("node `%s` is not listening on a tcp port", node.ID())
}

// boundHTTPPort is the port a service's http listener took, for the services
// that have one. The second return says whether this service serves http at
// all — most do not, and a universe reports ports only for those that do.
//
// THE SERVICE IS ASKED, because it is the only one who knows: dream hands it a
// listen address with port 0 and the kernel answers at bind time, inside the
// service's own http stack.
func boundHTTPPort(service commonIface.Service) (int, bool) {
	serving, ok := service.(interface{ Http() httpIface.Service })
	if !ok || serving.Http() == nil {
		return 0, false
	}

	address, err := serving.Http().GetListenAddress()
	if err != nil {
		return 0, false
	}

	port, err := strconv.Atoi(address.Port())
	if err != nil || port == 0 {
		return 0, false
	}

	return port, true
}

// GetCacheFolder returns the cache folder for the dream
// It can be overridden for testing purposes
var GetCacheFolder = func(multiverse string) (string, error) {
	suffix := "-" + strings.ToLower(multiverse)
	if multiverse == DefaultMultiverseName {
		suffix = ""
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return path.Join(home, DefaultMultiverseCacheFolder+suffix), nil
}

func generateDeterministicDVKeys(input string) ([]byte, []byte, error) {
	// Create deterministic seed from input string
	hash := sha256.Sum256([]byte(input))
	seed := hash[:]

	// Create deterministic random source
	randSource := mrand.New(mrand.NewSource(int64(binary.BigEndian.Uint64(seed[:8]))))

	priv, err := ecdsa.GenerateKey(elliptic.P256(), randSource)
	if err != nil {
		return nil, nil, fmt.Errorf("generate ecdsa key failed with: %s", err)
	}
	privateKey, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal private key failed with: %s", err)
	}
	privateKeyPEM := &pem.Block{Type: "EC PRIVATE KEY", Bytes: privateKey}
	var private bytes.Buffer
	err = pem.Encode(&private, privateKeyPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("pem encode private key failed with:  %s", err)
	}
	publicKey, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal public key failed with: %s", err)
	}
	publicKeyPEM := &pem.Block{Type: "PUBLIC KEY", Bytes: publicKey}
	var public bytes.Buffer
	err = pem.Encode(&public, publicKeyPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("failed encoding public key with: %s", err)
	}
	return private.Bytes(), public.Bytes(), nil
}
