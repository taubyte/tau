package vm

import (
	"time"

	"github.com/hashicorp/go-plugin"
)

var (
	ProcWatchInterval = time.Second
)

func HandShake() plugin.HandshakeConfig {
	return plugin.HandshakeConfig{
		ProtocolVersion:  1,
		MagicCookieKey:   "VM_ORBIT_SATELLITE",
		MagicCookieValue: "taubyte",
	}
}
