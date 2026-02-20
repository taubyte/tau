package core

// RootlessMode specifies how rootless container mode should be handled
type RootlessMode int

const (
	// RootlessModeAuto automatically detects rootless mode based on user privileges
	RootlessModeAuto RootlessMode = iota
	// RootlessModeEnabled forces rootless mode (fails if running as root)
	RootlessModeEnabled
	// RootlessModeDisabled forces root mode (runs as root)
	RootlessModeDisabled
)

// String returns a string representation of the RootlessMode
func (rm RootlessMode) String() string {
	switch rm {
	case RootlessModeAuto:
		return "auto"
	case RootlessModeEnabled:
		return "enabled"
	case RootlessModeDisabled:
		return "disabled"
	default:
		return "unknown"
	}
}

// BackendConfig is the interface that all backend configs must implement
type BackendConfig interface {
	BackendType() BackendType
}

// ContainerdConfig contains configuration for the containerd backend
type ContainerdConfig struct {
	// SocketPath is the path to the containerd socket
	// If empty, auto-detects based on platform and rootless mode
	// In rootless mode, defaults to ~/.local/share/containerd/containerd.sock
	SocketPath string
	// Namespace is the containerd namespace to use
	Namespace string
	// RootlessMode specifies how rootless container mode should be handled
	// Defaults to RootlessModeAuto (auto-detect based on user privileges)
	RootlessMode RootlessMode
	// RootlesskitPath is the path to rootlesskit binary (auto-detected if empty)
	RootlesskitPath string
	// FuseOverlayfsPath is the path to fuse-overlayfs binary (auto-detected if empty)
	FuseOverlayfsPath string
	// AutoStart enables automatic containerd startup if not running
	// If true and containerd is not available, starts a rootless containerd instance
	// Socket will be created in user home directory (~/.local/share/containerd/containerd.sock)
	AutoStart bool
	// ContainerdPath is the path to containerd binary (auto-detected if empty)
	ContainerdPath string
}

func (c ContainerdConfig) BackendType() BackendType { return BackendTypeContainerd }

// DockerConfig contains configuration for the Docker backend
type DockerConfig struct {
	// Host is the Docker daemon host/socket path
	// If empty, defaults to DOCKER_HOST environment variable or /var/run/docker.sock
	Host string
	// APIVersion is the Docker API version to use
	// If empty, uses API version negotiation
	APIVersion string
}

func (d DockerConfig) BackendType() BackendType { return BackendTypeDocker }

// FirecrackerConfig contains configuration for the Firecracker backend
type FirecrackerConfig struct {
	// SocketPath is the path to the Firecracker socket
	SocketPath string
	// AutoDownload automatically downloads Firecracker binary if not found
	AutoDownload bool
	// Version is the Firecracker version to use (e.g., "v1.4.0")
	// If empty, uses latest stable
	Version string
	// BinaryPath is the path to the Firecracker binary
	// If empty and AutoDownload is true, downloads to cache
	BinaryPath string
}

func (f FirecrackerConfig) BackendType() BackendType { return BackendTypeFirecracker }

// NanosConfig contains configuration for the Nanos backend
type NanosConfig struct {
	// ConfigPath is the path to the OPS config file (JSON)
	ConfigPath string
	// WorkDir is the working directory for OPS operations
	WorkDir string
}

func (n NanosConfig) BackendType() BackendType { return BackendTypeNanos }
