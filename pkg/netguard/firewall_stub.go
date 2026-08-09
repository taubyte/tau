//go:build !linux

package netguard

import "errors"

// errUnsupported is returned by the firewall installers on non-Linux hosts. tau
// in production runs on Linux; the build-container egress firewall is a Linux
// (nftables) feature, so on any other OS these fail closed rather than run a
// build unprotected.
var errUnsupported = errors.New("netguard: build egress firewall is only supported on linux")

// InstallDockerBridgeFilter is a no-op that fails closed off Linux.
func InstallDockerBridgeFilter() error { return errUnsupported }

// InstallCgroupFilter is a no-op that fails closed off Linux.
func InstallCgroupFilter(cgroupID uint64, level uint32) error { return errUnsupported }
