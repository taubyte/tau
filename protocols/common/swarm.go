package common

import "github.com/libp2p/go-libp2p/core/pnet"

func SwarmKey() pnet.PSK {
	return []byte(`/key/swarm/psk/1.0.0/
/base16/
a0205de90aece618d85c37401e84f43f39cebc05a03fec19c1c54ead5927f3ef`)
}
