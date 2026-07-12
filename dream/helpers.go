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
)

// GetFreePorts reserves n distinct ports from the kernel: it binds n TCP
// listeners on :0 (held simultaneously so they're distinct) and verifies UDP
// is bindable on each (seer serves DNS over UDP). Listeners are released on
// return; the tiny release-to-rebind window is covered by bind-retry at the
// call sites.
func GetFreePorts(n int) ([]int, error) {
	var (
		tcpListeners []net.Listener
		udpConns     []*net.UDPConn
	)
	defer func() {
		for _, l := range tcpListeners {
			l.Close()
		}
		for _, c := range udpConns {
			c.Close()
		}
	}()

	ports := make([]int, 0, n)
	for len(ports) < n {
		var bound bool
		for range 10 {
			l, err := net.Listen("tcp", DefaultHost+":0")
			if err != nil {
				return nil, fmt.Errorf("listening on a free tcp port failed with: %w", err)
			}

			port := l.Addr().(*net.TCPAddr).Port

			udpAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", DefaultHost, port))
			if err != nil {
				l.Close()
				continue
			}

			udpConn, err := net.ListenUDP("udp", udpAddr)
			if err != nil {
				// port not bindable on udp (e.g. taken by another process); release and retry
				l.Close()
				continue
			}

			tcpListeners = append(tcpListeners, l)
			udpConns = append(udpConns, udpConn)
			ports = append(ports, port)
			bound = true
			break
		}
		if !bound {
			return nil, fmt.Errorf("failed to reserve a free tcp+udp port after 10 attempts")
		}
	}

	return ports, nil
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
