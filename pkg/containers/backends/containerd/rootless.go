//go:build linux

package containerd

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"syscall"

	"github.com/taubyte/tau/pkg/containers/core"
)

// RootlessManager handles rootless mode operations and UID/GID mapping
type RootlessManager struct {
	config            core.ContainerdConfig
	rootlesskitPath   string
	fuseOverlayfsPath string
}

// NewRootlessManager creates a new rootless manager
func NewRootlessManager(config core.ContainerdConfig) (*RootlessManager, error) {
	rm := &RootlessManager{
		config: config,
	}

	if err := rm.detectTools(); err != nil {
		return nil, fmt.Errorf("failed to detect rootless tools: %w", err)
	}

	return rm, nil
}

// detectTools detects available rootless tools
func (rm *RootlessManager) detectTools() error {
	if rm.config.RootlesskitPath != "" {
		rm.rootlesskitPath = rm.config.RootlesskitPath
	} else if path, err := exec.LookPath("rootlesskit"); err == nil {
		rm.rootlesskitPath = path
	}

	if rm.config.FuseOverlayfsPath != "" {
		rm.fuseOverlayfsPath = rm.config.FuseOverlayfsPath
	} else if path, err := exec.LookPath("fuse-overlayfs"); err == nil {
		rm.fuseOverlayfsPath = path
	}

	return nil
}

// hasRootlesskit returns true if rootlesskit is available
func (rm *RootlessManager) hasRootlesskit() bool {
	return rm.rootlesskitPath != ""
}

// hasFuseOverlayfs returns true if fuse-overlayfs is available
func (rm *RootlessManager) hasFuseOverlayfs() bool {
	return rm.fuseOverlayfsPath != ""
}

// validateUIDGIDMapping validates that subuid/subgid mappings are configured for rootless operations
func (rm *RootlessManager) validateUIDGIDMapping() error {
	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("failed to get current user: %w", err)
	}

	if err := rm.hasSubIDMapping("/etc/subuid", currentUser.Username); err != nil {
		return fmt.Errorf("subuid mapping validation failed: %w", err)
	}

	if err := rm.hasSubIDMapping("/etc/subgid", currentUser.Username); err != nil {
		return fmt.Errorf("subgid mapping validation failed: %w", err)
	}

	return nil
}

// hasSubIDMapping checks if subuid/subgid mapping exists for a user
func (rm *RootlessManager) hasSubIDMapping(file, username string) error {
	content, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", file, err)
	}

	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Split(line, ":")
		if len(parts) >= 3 && parts[0] == username {
			return nil
		}
	}

	return fmt.Errorf("no subuid/subgid mapping found for user %s in %s", username, file)
}

// validateMountPermissions validates if a mount operation will work with current UID/GID mapping
func (rm *RootlessManager) validateMountPermissions(hostPath, containerPath string) error {
	info, err := os.Stat(hostPath)
	if err != nil {
		return fmt.Errorf("cannot stat host path %s: %w", hostPath, err)
	}

	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("failed to get current user: %w", err)
	}

	uid := info.Sys().(*syscall.Stat_t).Uid
	gid := info.Sys().(*syscall.Stat_t).Gid

	currentUID, err := strconv.Atoi(currentUser.Uid)
	if err != nil {
		return fmt.Errorf("invalid current UID: %w", err)
	}

	currentGID, err := strconv.Atoi(currentUser.Gid)
	if err != nil {
		return fmt.Errorf("invalid current GID: %w", err)
	}

	if int(uid) != currentUID && int(gid) != currentGID {
		if err := rm.canMapUID(uid); err != nil {
			if rm.hasRootlesskit() {
				return fmt.Errorf("cannot mount %s: UID %d cannot be mapped even with rootlesskit (subuid not configured): %w",
					hostPath, uid, err)
			}
			return fmt.Errorf("cannot mount %s: UID %d cannot be mapped (subuid not configured): %w",
				hostPath, uid, err)
		}
		if err := rm.canMapGID(gid); err != nil {
			if rm.hasRootlesskit() {
				return fmt.Errorf("cannot mount %s: GID %d cannot be mapped even with rootlesskit (subgid not configured): %w",
					hostPath, gid, err)
			}
			return fmt.Errorf("cannot mount %s: GID %d cannot be mapped (subgid not configured): %w",
				hostPath, gid, err)
		}
	}

	return nil
}

// canMapUID checks if a UID can be mapped using subuid configuration
func (rm *RootlessManager) canMapUID(targetUID uint32) error {
	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("failed to get current user: %w", err)
	}

	mappings, err := rm.readSubIDMappings("/etc/subuid", currentUser.Username)
	if err != nil {
		return err
	}

	for _, mapping := range mappings {
		if targetUID >= mapping.Start && targetUID < mapping.Start+mapping.Count {
			return nil
		}
	}

	return fmt.Errorf("UID %d not in subuid range for user %s", targetUID, currentUser.Username)
}

// canMapGID checks if a GID can be mapped using subgid configuration
func (rm *RootlessManager) canMapGID(targetGID uint32) error {
	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("failed to get current user: %w", err)
	}

	mappings, err := rm.readSubIDMappings("/etc/subgid", currentUser.Username)
	if err != nil {
		return err
	}

	for _, mapping := range mappings {
		if targetGID >= mapping.Start && targetGID < mapping.Start+mapping.Count {
			return nil
		}
	}

	return fmt.Errorf("GID %d not in subgid range for user %s", targetGID, currentUser.Username)
}

// readSubIDMappings reads subuid/subgid mappings for a user
func (rm *RootlessManager) readSubIDMappings(file, username string) ([]subIDMapping, error) {
	content, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", file, err)
	}

	var mappings []subIDMapping
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Split(line, ":")
		if len(parts) >= 3 && parts[0] == username {
			start, err := strconv.ParseUint(parts[1], 10, 32)
			if err != nil {
				continue
			}
			count, err := strconv.ParseUint(parts[2], 10, 32)
			if err != nil {
				continue
			}
			mappings = append(mappings, subIDMapping{
				Start: uint32(start),
				Count: uint32(count),
			})
		}
	}

	return mappings, nil
}

// subIDMapping represents a subuid/subgid mapping range
type subIDMapping struct {
	Start uint32
	Count uint32
}
