package reset

import (
	"fmt"

	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/tools/dream/cli/command"
	"github.com/taubyte/tau/tools/dream/cli/common"
	"github.com/urfave/cli/v2"
)

// Command deletes one kept universe from disk, so its next start is empty.
func Command(ctx *common.Context) *cli.Command {
	c := &cli.Command{
		Name:     "reset",
		Usage:    "Delete a kept universe and its data, so its next start is empty",
		Category: "Start & stop",
		Description: `Deletes a universe kept on disk (default: "default"). Refuses while that universe
is loaded in the running Dream; use dream new universe --fresh for that case.

Example:
   dream reset blackhole`,
		Action: func(c *cli.Context) error {
			name := c.String("name")
			// deleting the root under live nodes would corrupt them
			if _, err := ctx.Multiverse.Universe(name).Status(); err == nil {
				return fmt.Errorf("universe `%s` is loaded in the running Dream; use `dream new universe %s --fresh`", name, name)
			}
			// shortcut: only the default multiverse; add a flag if named multiverses need it
			return dream.Reset(dream.DefaultMultiverseName, name)
		},
	}

	command.NameWithDefault(c, dream.DefaultUniverseName)

	return c
}
