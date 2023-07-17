package engine

import (
	"github.com/multiformats/go-multicodec"
)

const (
	Version = 0x01
	Codec   = multicodec.Cbor
)

var (
	Prefix = []string{"tns", "v1"}
)
