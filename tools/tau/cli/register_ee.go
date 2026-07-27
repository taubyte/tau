//go:build ee

package cli

import eecli "github.com/taubyte/tau/ee/tools/tau/cli"

// Adds the enterprise CLI commands in -tags ee builds. The command set and its
// implementations both live in ee; this seam only appends what that package
// hands back, so nothing about their layout is fixed here.
func init() {
	extraCommands = append(extraCommands, eecli.Commands()...)
}
