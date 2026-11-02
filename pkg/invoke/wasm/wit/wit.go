// Package wit is the WIT of every host interface tau serves, as a guest's toolchain
// reads it from this directory and a host module reads it from FS.
package wit

import "embed"

//go:embed handler.wit deps
var FS embed.FS
