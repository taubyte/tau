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
// mount the rule would not work, so a restricted build must fail closed rather
// than run behind a firewall that matches nothing.
func cgroupV2Unified() bool {
	var st unix.Statfs_t
	if err := unix.Statfs(cgroupRoot, &st); err != nil {
		return false
	}
	return st.Type == unix.CGROUP2_SUPER_MAGIC
}

// ensureCgroupEgressFilter creates (if needed) the build-namespace parent cgroup
// and installs — or idempotently re-asserts — the single egress rule that drops
// denied-destination traffic from every socket under that cgroup. Because build
// containers are pinned under "/<namespace>/<id>" (oci.WithCgroup in Create),
// one parent-level rule covers all of them, including BuildKit's nested runc
// cgroups. Fail-closed: a non-nil error must abort container creation.
//
// The `level` is computed assuming tau runs in the root cgroup namespace (the
// normal case for a node daemon), so the namespace's path depth is its absolute
// ancestor level. If tau is itself in a nested cgroup namespace the level would
// be off and the rule would match nothing — this, and the `socket cgroupv2`
// semantics generally, MUST be validated end-to-end in vagrant/CI (see
// netguard.InstallCgroupFilter).
func ensureCgroupEgressFilter(namespace string) error {
	if namespace == "" {
		return fmt.Errorf("restricted egress requires a non-empty containerd namespace")
	}
	if !cgroupV2Unified() {
		return fmt.Errorf("restricted egress needs the cgroup v2 unified hierarchy at %s; refusing to fail open", cgroupRoot)
	}

	parent := filepath.Join(cgroupRoot, namespace)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("creating build cgroup %s: %w", parent, err)
	}

	var st unix.Stat_t
	if err := unix.Stat(parent, &st); err != nil {
		return fmt.Errorf("stat build cgroup %s: %w", parent, err)
	}

	// level = number of path components of the parent cgroup, counted from the
	// cgroup root (e.g. namespace "taubyte" -> "/taubyte" -> level 1).
	level := uint32(len(strings.Split(strings.Trim(namespace, "/"), "/")))
	return netguard.InstallCgroupFilter(st.Ino, level)
}
