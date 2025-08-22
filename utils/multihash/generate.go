package multihash

import (
	mh "github.com/ipsn/go-ipfs/gxlibs/github.com/multiformats/go-multihash"
)

// Hash returns the hash of a string or []byte using B58
func Hash[v string | []byte](value v) string {
	hash, _ := mh.Sum([]byte(value), mh.SHA2_256, -1)
	return hash.B58String()
}
