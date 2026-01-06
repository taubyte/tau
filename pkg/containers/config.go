package containers

import (
	"github.com/taubyte/tau/pkg/containers/core"
)

// Re-export core config types for backward compatibility
type (
	BackendConfig     = core.BackendConfig
	ContainerdConfig  = core.ContainerdConfig
	DockerConfig      = core.DockerConfig
	FirecrackerConfig = core.FirecrackerConfig
	NanosConfig       = core.NanosConfig
	RootlessMode      = core.RootlessMode
)

// Re-export core constants
const (
	RootlessModeAuto     = core.RootlessModeAuto
	RootlessModeEnabled  = core.RootlessModeEnabled
	RootlessModeDisabled = core.RootlessModeDisabled
)
