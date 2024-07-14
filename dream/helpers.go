package dream

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	mrand "math/rand"
	"net"
	"os"
	"path"
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
		lastUniversePortShift += int(mrand.NewSource(time.Now().UnixNano()).Int63()%int64(maxUniverses)) * portsPerUniverse
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

func getCacheFolder() (string, error) {
	cacheFolder := ".cache/dreamland"

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return path.Join(home, cacheFolder), nil
}

func generateDVKeys() ([]byte, []byte, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
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
