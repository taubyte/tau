//go:build ee

package app

import "github.com/taubyte/tau/pkg/config"

// checkStartable refuses nothing here: this build's auth reads a tenancy but
// does not require one.
func checkStartable(config.Config) error { return nil }
