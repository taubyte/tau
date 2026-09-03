package forgetest

import "github.com/taubyte/tau/pkg/v2/forge/forgetest/hermetic"

// EVERY BINARY THAT RUNS THIS SUITE READS SOURCE UNDER AN ENVIRONMENT NOBODY
// HAD TO REMEMBER TO SET.
//
// Both drivers' route tables are closed by a test that type-checks the driver's
// own source, and both of those loads used to run under whatever environment
// the machine happened to have: measured, a GOWORK naming a file that is not a
// workspace kills both of them, while the ledger's own load, which sets
// GOWORK=off, does not notice it. Writing the environment into each config is a
// fix for the configs that exist; an environment is a property of the process,
// so it is set once, here, for the whole binary — and a driver's tests get it
// by running the conformance suite at all.
func init() { hermetic.Pin() }
