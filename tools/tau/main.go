package main

import (
	"fmt"
	"io"
	"log"
	"os"

	"github.com/taubyte/tau/pkg/cli/i18n"
	"github.com/taubyte/tau/tools/tau/cli"
	"github.com/taubyte/tau/tools/tau/friendly"
)

func main() {
	err := friendly.Error(cli.Run(os.Args...))
	if err != nil {
		if friendly.Debug {
			log.Fatal(i18n.AppCrashed(err))
		}
		fail(os.Stderr, err)
		os.Exit(1)
	}
}

// fail prints the error as the user should read it: no log date or prefix.
func fail(w io.Writer, err error) { fmt.Fprintln(w, err) }
