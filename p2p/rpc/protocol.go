package rpc

import (
	"strings"

	"github.com/libp2p/go-libp2p/core/protocol"
)

// Protocol derives the libp2p protocol id a v2 service listens on from a
// generated connect service name — "/" plus the proto package with dots as
// slashes:
//
//	rpc.Protocol(authv2connect.ProjectServiceName) // "tau.auth.v2.ProjectService" -> "/tau/auth/v2"
//
// The id belongs to the PACKAGE, not to any one service in it, which is why
// any of the package's names may be passed: auth mounts five service blocks
// behind one mux on one listener, and all five share the package that names
// it. Passing a name is what makes the id underivable from anything but the
// schema — the id and the wire surface then cannot drift apart, because
// there is only one place either is written down.
//
// v1 spells its ids out as free-standing string vars beside the service name
// (services/common/names.go), tied to no message definition, which is how
// "/tns/v1" survived a run of wire changes without a bump. It is also why
// every v2 package carries the "tau." prefix: v1 answers on "/auth/v1", and
// the two coexist in one swarm and one binary for the whole span between the
// first v2 service and the swap. pkg/proto's TestTheRulesBufCannotCheck
// checks that no v2 package derives an id v1 already answers on.
func Protocol(serviceName string) protocol.ID {
	pkg := serviceName
	if i := strings.LastIndex(serviceName, "."); i >= 0 {
		pkg = serviceName[:i]
	}
	return protocol.ID("/" + strings.ReplaceAll(pkg, ".", "/"))
}
