package common

import "golang.org/x/exp/slices"

// ServiceCapabilities declares which shared resources a registered service takes
// part in, so the node and Dream provision the right things for it:
//   - HTTP: added to HTTPServices, so the shared HTTP server is created for shapes
//     that include this service (see cli/node/start.go) and cfg.Http() is non-nil.
//   - P2PStream: added to P2PStreamServices (p2p command/stream handlers).
//   - Client: added to Clients (a typed p2p client other nodes can dial).
type ServiceCapabilities struct {
	HTTP      bool
	P2PStream bool
	Client    bool
}

// RegisterService makes name a known service so shapes can reference it and the
// node/Dream can provision its resources. It is the extension point for
// build-tag-gated services (e.g. ee) that register themselves from an init().
// It is idempotent: a name already known is left untouched, so registration is
// safe to reach through more than one import path.
func RegisterService(name string, caps ServiceCapabilities) {
	if name == "" || slices.Contains(Services, name) {
		return
	}
	Services = append(Services, name)
	if caps.HTTP {
		HTTPServices = append(HTTPServices, name)
	}
	if caps.P2PStream {
		P2PStreamServices = append(P2PStreamServices, name)
	}
	if caps.Client {
		Clients = append(Clients, name)
	}
}
