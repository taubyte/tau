package main

import (
	"log"
	"os"

	"github.com/taubyte/tau/pkg/cli/i18n"
	"github.com/taubyte/tau/tools/tau/cli"
	"github.com/taubyte/tau/tools/tau/friendly"
)

func main() {
	err := friendly.Error(cli.Run(os.Args...))
	if err != nil {
		log.Fatal(i18n.AppCrashed(err))
	}
}
