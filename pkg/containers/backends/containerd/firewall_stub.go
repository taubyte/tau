//go:build !linux

package containerd

import (
	"errors"
	"os"
)

var errNotLinux = errors.New("restricted egress firewall is only supported on linux")

// ensureCgroupEgressFilter fails closed off Linux: the cgroup/nftables egress
// firewall is a Linux feature and a restricted container must not run without it.
func ensureCgroupEgressFilter() error { return errNotLinux }

func containerCgroupPath(string, bool) (string, error) { return "", errNotLinux }

func restrictedCgroupParent() (string, error) { return "", errNotLinux }

func restrictedCgroupDir() (*os.File, error) { return nil, errNotLinux }

func resourceLimitsAvailable() bool { return false }
