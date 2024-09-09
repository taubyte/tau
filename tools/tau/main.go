package main

import (
	"log"
	"os"

	"github.com/taubyte/tau/pkg/cli/i18n"
	"github.com/taubyte/tau/tools/tau/cli"
)

func main() {
	err := cli.Run(os.Args...)
	if err != nil {
		log.Fatal(i18n.AppCrashed(err))
	}
}
