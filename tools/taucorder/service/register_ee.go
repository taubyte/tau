//go:build ee

package main

import eetaucorder "github.com/taubyte/tau/ee/taucorder"

// registerExtraHandlers mounts the ee taucorder handlers. Structural only —
// which handlers exist, and what they serve, is decided on the other side.
func registerExtraHandlers() {
	eetaucorder.Register()
}
