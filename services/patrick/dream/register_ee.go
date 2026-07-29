//go:build ee

package dream

// Registers the dream wiring for services this build provides.
import (
	_ "github.com/taubyte/tau/ee/services/accounts/dream"
)
