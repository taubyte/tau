// Package hosted is the build service as an instance runs it: the only part of a
// cloud forges and people reach. It is built here, since the package serving its
// routes may not import the forges and the p2p it is reached through
// (services/build/boundary_test.go).
package hosted

import "github.com/taubyte/tau/core/service"

// New is the build service, unstarted.
func New() service.Service { return &server{} }
