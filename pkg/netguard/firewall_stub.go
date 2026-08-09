//go:build !linux

package netguard

import "errors"

// errUnsupported is returned by the firewall installers on non-Linux hosts. tau
// in production runs on Linux; the container egress firewall is a Linux
// (nftables) feature, so on any other OS these fail closed rather than run an
// untrusted container unprotected.
var errUnsupported = errors.New("netguard: container egress firewall is only supported on linux")

// InstallBridgeFilter is a no-op that fails closed off Linux.
func InstallBridgeFilter(bridge string) error { return errUnsupported }

// InstallCgroupFilter is a no-op that fails closed off Linux.
func InstallCgroupFilter(cgroupID uint64, level uint32) error { return errUnsupported }

// Uninstall has nothing to remove off Linux.
func Uninstall() error { return nil }
