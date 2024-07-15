package flags

import (
	"strings"

	"github.com/urfave/cli/v2"
)

// ToUpper takes a slice of StringFlag sets the values each flag to upper case if the flag is set
func ToUpper(c *cli.Context, flags ...*cli.StringFlag) {
	for _, flag := range flags {
		if c.IsSet(flag.Name) {
			err := c.Set(flag.Name, strings.ToUpper(c.String(flag.Name)))
			if err != nil {
				panic(err)
			}
		}
	}
}
