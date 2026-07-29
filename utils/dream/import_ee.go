//go:build ee

package dream

import eedream "github.com/taubyte/tau/ee/dream"

// Adds the dream registrations this build provides. Structural only — what
// they are, and how they are laid out, is decided on the other side.
func init() { eedream.Register() }
