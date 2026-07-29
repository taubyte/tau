//go:build ee

package main

// Registers the dream wiring for services this build provides.
import (
	_ "github.com/taubyte/tau/ee/clients/p2p/accounts/dream"
	_ "github.com/taubyte/tau/ee/services/accounts/dream"
)
