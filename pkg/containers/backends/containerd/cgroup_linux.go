//go:build linux

package containerd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

const cgroupRoot = "/sys/fs/cgroup"

// The two children tau makes of the cgroup it owns.
//
// Everything untrusted runs under restricted: the rootless daemon, so the
// sockets it forwards on a container's behalf are covered, and each container,
// so it can be given resource limits. One nftables rule matched at restricted's
// depth therefore covers both, whichever mode the backend is in.
//
// main exists because of the kernel's no-internal-process rule: controllers
// cannot be enabled for children while the parent still holds processes, so tau
// moves its own there and leaves the parent empty.
const (
	restrictedCgroup = "restricted"
	containersCgroup = "containers"
	mainCgroup       = "main"
	daemonCgroup     = "daemon"
)

// cgroupTree is the subtree tau owns and may create children in.
type cgroupTree struct {
	// root is the path from the cgroup root, e.g. "/system.slice/tau.service".
	root string
	// controllers are those it managed to enable for its children.
	controllers []string
}

// restrictedPath is where every untrusted workload is placed.
func (t *cgroupTree) restrictedPath() string {
	return joinCgroup(t.root, restrictedCgroup)
}

// daemonPath is where the rootless daemon goes: inside restricted, so the rule
// covers the sockets it forwards, but in a leaf of its own. A cgroup may hold
// processes or give its children controllers, never both, and containers become
// children of restricted the moment one is created with a limit.
func (t *cgroupTree) daemonPath() string {
	return joinCgroup(t.restrictedPath(), daemonCgroup)
}

// containerPath is where a container that asked for nothing goes: a sibling of
// restricted, so the egress rule — which matches the restricted subtree — does
// not reach it. Ordinary workloads on the node are not collateral.
func (t *cgroupTree) containerPath(id string) string {
	return joinCgroup(t.root, containersCgroup, id)
}

// level is the restricted cgroup's depth from the cgroup root, which is what
// the nftables socket match counts.
func (t *cgroupTree) level() uint32 {
	return uint32(len(strings.Split(strings.Trim(t.restrictedPath(), "/"), "/")))
}

// limitsAvailable reports whether children can be given resource limits, which
// needs the controllers to have been delegated and enabled.
func (t *cgroupTree) limitsAvailable() bool {
	return len(t.controllers) > 0
}

func joinCgroup(parts ...string) string {
	return "/" + strings.Join(strings.FieldsFunc(filepath.Join(parts...), func(r rune) bool { return r == '/' }), "/")
}

var (
	treeOnce sync.Once
	tree     *cgroupTree
	treeErr  error
)

// cgroups prepares, once, the subtree tau owns, and returns it.
//
// A node daemon started by systemd with Delegate=yes owns its own unit cgroup
// and may create children in it unprivileged; running as root, any cgroup can be
// created. Without either, there is nothing to place a workload in and no way to
// tell one from another, so this reports why rather than carrying on.
func cgroups() (*cgroupTree, error) {
	treeOnce.Do(func() { tree, treeErr = prepareCgroupTree() })
	return tree, treeErr
}

func prepareCgroupTree() (*cgroupTree, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(cgroupRoot, &st); err != nil || st.Type != unix.CGROUP2_SUPER_MAGIC {
		return nil, fmt.Errorf("%s is not the cgroup v2 unified hierarchy", cgroupRoot)
	}
	if err := inRootCgroupNamespace(); err != nil {
		return nil, err
	}

	root, err := selfCgroup()
	if err != nil {
		return nil, err
	}

	// At the very root of the hierarchy there is nothing delegated to us and
	// nothing to vacate, so tau takes a subtree of its own. Only root can.
	if root == "/" {
		root = joinCgroup(defaultNamespace)
	}

	t := &cgroupTree{root: root}

	for _, child := range []string{mainCgroup, containersCgroup, joinCgroup(restrictedCgroup, daemonCgroup)} {
		if err := os.MkdirAll(filepath.Join(cgroupRoot, root, child), 0o755); err != nil {
			return nil, fmt.Errorf("tau cannot create cgroups under %s, so an untrusted workload cannot be confined: %w", root, err)
		}
	}

	t.controllers = enableControllers(root)
	return t, nil
}

// enableControllers hands tau's controllers down to its children, vacating the
// parent first, and returns those that took. Resource limits are the only thing
// that depends on this, so a failure is reported by the empty list rather than
// as an error: confinement still works without it.
func enableControllers(root string) []string {
	dir := filepath.Join(cgroupRoot, root)

	available, err := os.ReadFile(filepath.Join(dir, "cgroup.controllers"))
	if err != nil {
		return nil
	}

	wanted := map[string]bool{"memory": true, "pids": true, "cpu": true}
	var request []string
	for _, controller := range strings.Fields(string(available)) {
		if wanted[controller] {
			request = append(request, "+"+controller)
		}
	}
	if len(request) == 0 {
		return nil
	}

	// The parent may hold no processes while its children have controllers, so
	// whatever is here — tau itself — moves down one.
	if procs, err := os.ReadFile(filepath.Join(dir, "cgroup.procs")); err == nil {
		for _, pid := range strings.Fields(string(procs)) {
			os.WriteFile(filepath.Join(dir, mainCgroup, "cgroup.procs"), []byte(pid), 0o644)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "cgroup.subtree_control"), []byte(strings.Join(request, " ")), 0o644); err != nil {
		return nil
	}

	enabled, err := os.ReadFile(filepath.Join(dir, "cgroup.subtree_control"))
	if err != nil {
		return nil
	}
	return strings.Fields(string(enabled))
}

// selfCgroup returns the cgroup this process is in, as a path from the cgroup
// root. cgroup v2 reports it on a single "0::<path>" line.
func selfCgroup() (string, error) {
	content, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", fmt.Errorf("reading /proc/self/cgroup: %w", err)
	}

	for _, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		if own, ok := strings.CutPrefix(line, "0::"); ok {
			return joinCgroup(own), nil
		}
	}
	return "", fmt.Errorf("/proc/self/cgroup has no cgroup v2 entry; refusing to fail open")
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
