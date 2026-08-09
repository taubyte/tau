//go:build linux

package containerd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/taubyte/tau/pkg/netguard"
	"golang.org/x/sys/unix"
)

const cgroupRoot = "/sys/fs/cgroup"

// cgroupV2Unified reports whether /sys/fs/cgroup is the cgroup v2 unified
// hierarchy. `socket cgroupv2` matching requires it; on cgroup v1 or a hybrid
// mount the rule would not work, so a restricted container must fail closed
// rather than run behind a firewall that matches nothing.
func cgroupV2Unified() bool {
	var st unix.Statfs_t
	if err := unix.Statfs(cgroupRoot, &st); err != nil {
		return false
	}
	return st.Type == unix.CGROUP2_SUPER_MAGIC
}

// restrictedCgroup is the sub-cgroup every egress-restricted container is pinned
// under, inside the namespace's own subtree. It exists so the filter has
// something to match that means "restricted" and nothing else: containerd's
// default cgroup for a container is "/<namespace>/<id>", so matching the
// namespace itself would firewall every unrestricted container in it too.
const restrictedCgroup = "restricted"

// restrictedCgroupPath is the cgroup a restricted container is pinned under,
// as the OCI spec spells it (absolute, from the cgroup root).
func restrictedCgroupPath(namespace, id string) string {
	return "/" + namespace + "/" + restrictedCgroup + "/" + id
}

// ensureCgroupEgressFilter creates (if needed) the parent cgroup shared by
// restricted containers and installs — or idempotently re-asserts — the single
// egress rule that drops denied-destination traffic from every socket under it.
// Because restricted containers are pinned under
// "/<namespace>/restricted/<id>" (oci.WithCgroup in Create), one parent-level
// rule covers all of them, including any nested cgroups their workload spawns
// (BuildKit's runc children, for one). Fail-closed: a non-nil error must abort
// container creation.
//
// The `level` is counted from the cgroup root, which assumes tau runs in the
// root cgroup namespace — the normal case for a node daemon. If tau were itself
// in a nested cgroup namespace the level would be off and the rule would match
// nothing, so this fails closed by refusing to install in that case.
func ensureCgroupEgressFilter(namespace string) error {
	if namespace == "" {
		return fmt.Errorf("restricted egress requires a non-empty containerd namespace")
	}
	if !cgroupV2Unified() {
		return fmt.Errorf("restricted egress needs the cgroup v2 unified hierarchy at %s; refusing to fail open", cgroupRoot)
	}
	if err := inRootCgroupNamespace(); err != nil {
		return err
	}

	parent := filepath.Join(cgroupRoot, namespace, restrictedCgroup)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("creating restricted-container cgroup %s: %w", parent, err)
	}

	var st unix.Stat_t
	if err := unix.Stat(parent, &st); err != nil {
		return fmt.Errorf("stat restricted-container cgroup %s: %w", parent, err)
	}

	// level = number of path components of the parent cgroup, counted from the
	// cgroup root (namespace "taubyte" -> "/taubyte/restricted" -> level 2).
	level := uint32(len(strings.Split(strings.Trim(namespace, "/"), "/")) + 1)
	return netguard.InstallCgroupFilter(st.Ino, level)
}

// inRootCgroupNamespace reports whether this process sees the whole cgroup
// hierarchy. The nftables match counts ancestor levels from the real root, so a
// tau confined to a nested cgroup namespace would compute a level that matches
// nothing — a rule that installs cleanly and filters nothing. Refuse instead.
//
// /proc/self/cgroup cannot answer this: a process in a private cgroup namespace
// reports "0::/" — the namespace root — which is exactly what a process at the
// real root reports. The mount tells them apart instead. Every cgroup carries a
// cgroup.type file except the true root, so finding one at the top of the
// mounted hierarchy means the top is not the top.
func inRootCgroupNamespace() error {
	if _, err := os.Stat(filepath.Join(cgroupRoot, "cgroup.type")); err == nil {
		return fmt.Errorf("restricted egress needs tau to see the whole cgroup hierarchy, but %s is a cgroup rather than the root of one (a nested cgroup namespace); refusing to fail open", cgroupRoot)
	}
	return nil
}
