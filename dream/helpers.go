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
	"net"
	"os"
	"path"
	"strings"
	"time"
)

func lastSimplePort() int {
	lastSimplePortAllocatedLock.Lock()
	defer lastSimplePortAllocatedLock.Unlock()
	lastSimplePortAllocated++
	return lastSimplePortAllocated
}

func lastPortShift() int {
	lastUniversePortShiftLock.Lock()
	defer lastUniversePortShiftLock.Unlock()
	for {
		lastUniversePortShift += (int(mrand.NewSource(time.Now().UnixNano()).Int63()%int64(maxUniverses)) * portsPerUniverse) % 6000
		l, err := net.Listen("tcp", fmt.Sprintf("%s:%d", DefaultHost, lastUniversePortShift))
		if err == nil {
			l.Close()
			break
		}
	}
	return lastUniversePortShift
}

func afterStartDelay() time.Duration {
	rnd := mrand.New(mrand.NewSource(time.Now().UnixNano()))
	return time.Duration(BaseAfterStartDelay+rnd.Intn(MaxAfterStartDelay-BaseAfterStartDelay)) * time.Millisecond
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
