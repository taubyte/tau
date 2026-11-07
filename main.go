package main

import (
	"errors"
	"log"
	"os"

	cli "github.com/taubyte/tau/cli/app"
	"github.com/taubyte/tau/pkg/profiling"
)

func main() {
	profiling.Start()

	if err := cli.Run(os.Args...); err != nil {
		if errors.As(err, new(cli.ConfigError)) {
			log.Print(err)
			os.Exit(cli.ExitConfig)
		}
		log.Fatal(err)
	}
}
