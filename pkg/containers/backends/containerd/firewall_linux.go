//go:build linux

package containerd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/taubyte/tau/pkg/netguard"
	"golang.org/x/sys/unix"
)

// containerCgroupPath is the cgroup a container is pinned under, as the OCI spec
// spells it (absolute, from the cgroup root). Restricted workloads go under the
// subtree the egress rule covers; everything else goes beside it.
func containerCgroupPath(id string, restricted bool) (string, error) {
	tree, err := cgroups()
	if err != nil {
		return "", err
	}
	if restricted {
		return joinCgroup(tree.restrictedPath(), id), nil
	}
	return tree.containerPath(id), nil
}

// ensureCgroupEgressFilter installs — or idempotently re-asserts — the single
// egress rule that drops denied-destination traffic from every socket under
// tau's restricted cgroup. One rule covers every untrusted workload on the node,
// nested cgroups included, because the match asks for the ancestor at that
// depth: a container pinned under it matches, and so does the rootless daemon
// forwarding on a container's behalf. Fail-closed: a non-nil error must abort
// container creation.
func ensureCgroupEgressFilter() error {
	tree, err := cgroups()
	if err != nil {
		return err
	}

	var st unix.Stat_t
	if err := unix.Stat(filepath.Join(cgroupRoot, tree.restrictedPath()), &st); err != nil {
		return fmt.Errorf("stat restricted cgroup %s: %w", tree.restrictedPath(), err)
	}

	return netguard.InstallCgroupFilter(st.Ino, tree.level())
}

// restrictedCgroupParent is the cgroup a nested runtime must parent its own
// containers to, so they land inside what the egress rule covers.
func restrictedCgroupParent() (string, error) {
	tree, err := cgroups()
	if err != nil {
		return "", err
	}
	return tree.restrictedPath(), nil
}

// restrictedCgroupDir opens the restricted cgroup so a child process can be
// started directly inside it, which is how the rootless daemon — and therefore
// everything it forwards on a container's behalf — comes under the egress rule.
func restrictedCgroupDir() (*os.File, error) {
	tree, err := cgroups()
	if err != nil {
		return nil, err
	}
	return os.Open(filepath.Join(cgroupRoot, tree.daemonPath()))
}

// resourceLimitsAvailable reports whether tau can give a container limits.
func resourceLimitsAvailable() bool {
	tree, err := cgroups()
	return err == nil && tree.limitsAvailable()
}
