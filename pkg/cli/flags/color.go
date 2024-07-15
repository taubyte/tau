package flags

import (
	"fmt"
	"strings"

	slices "github.com/taubyte/utils/slices/string"
	"github.com/urfave/cli/v2"
)

const ColorNever = "never"

var ColorOptions = []string{"never", "auto"}

var Color = &cli.StringFlag{
	Name:        "color",
	DefaultText: "auto",
	EnvVars:     []string{"TAU_COLOR"},
}

func GetColor(c *cli.Context) (color string, err error) {
	if c.IsSet(Color.Name) {
		color = c.String(Color.Name)

		if !slices.Contains(ColorOptions, color) {
			return "", fmt.Errorf("color must be one of %s", strings.Join(ColorOptions, ", "))
		}
	}

	return
}
