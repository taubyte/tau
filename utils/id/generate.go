package id

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"time"

	mh "github.com/multiformats/go-multihash"
)

var (
	randomnessLength  = 32
	randomnessTimeout = 1 * time.Second
)

// getRandom generates a random buffer of length 32
func getRandom() []byte {
	ctx, ctx_cancel := context.WithTimeout(context.Background(), randomnessTimeout)
	defer ctx_cancel()
	for {
		select {
		case <-ctx.Done():
			return []byte{}
		default:
			r := make([]byte, randomnessLength)
			n, err := io.ReadFull(rand.Reader, r)
			if n == len(r) && err == nil {
				return r
			}
		}
	}
}

// Generate creates a hash ID from the given arguments.
//
// Provided parameters + current timestamp + randomness
func Generate(args ...interface{}) string {
	// check https://github.com/ipsn/go-ipfs/blob/master/gxlibs/github.com/libp2p/go-libp2p-peer/peer.go#L154 and https://github.com/ipsn/go-ipfs/blob/master/gxlibs/github.com/libp2p/go-libp2p-peer/peer.go#L39
	args = append(args, getRandom())
	args = append(args, time.Now().Unix())
	hash, _ := mh.Sum([]byte(fmt.Sprint(args...)), mh.SHA2_256, -1)
	return hash.B58String()
}
