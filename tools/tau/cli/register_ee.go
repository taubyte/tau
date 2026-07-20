//go:build ee

package cli

// Pulls in the build-tagged CLI subcommands so their init() registration runs.
import _ "github.com/taubyte/tau/ee/tools/tau/cli/commands/accounts"
