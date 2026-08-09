//go:build !linux

package containerd

import "errors"

// ensureCgroupEgressFilter fails closed off Linux: the cgroup/nftables egress
// firewall is a Linux feature and a restricted build must not run without it.
func ensureCgroupEgressFilter(namespace string) error {
	return errors.New("restricted egress firewall is only supported on linux")
}
