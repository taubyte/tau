//go:build !linux

package containerd

import "errors"

// ensureCgroupEgressFilter fails closed off Linux: the cgroup/nftables egress
// firewall is a Linux feature and a restricted container must not run without it.
func ensureCgroupEgressFilter(namespace string) error {
	return errors.New("restricted egress firewall is only supported on linux")
}

// restrictedCgroupPath is unreachable off Linux — ensureCgroupEgressFilter
// refuses first — but Create names it before it asks.
func restrictedCgroupPath(namespace, id string) string {
	return "/" + namespace + "/restricted/" + id
}
