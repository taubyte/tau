package autocomplete

import (
	"fmt"
	"os"
	"path/filepath"

	_ "embed"

	"github.com/urfave/cli/v2"
)

var (
	//go:embed bash_autocomplete.sh
	script string

	Command = &cli.Command{
		Name:   "autocomplete",
		Usage:  "Used with eval or in .bashrc for autocompletion",
		Action: Run,
	}
)

func Run(ctx *cli.Context) error {
	basePath := filepath.Base(os.Args[0])

	fmt.Println(script + basePath)

	return nil
}
