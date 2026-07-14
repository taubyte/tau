//go:build ee

package dream

// EE service Dream registrations, pulled in only under -tags ee (the companion
// to import.go). Never linked by the production binary — only Dream harnesses
// and tools import utils/dream. The ee submodule aggregates its own service set
// behind ee/dream, so tau references no ee layout.
import (
	_ "github.com/taubyte/tau/ee/dream"
)
