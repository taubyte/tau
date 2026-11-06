package main

import (
	"log"
	"os"

	cli "github.com/taubyte/tau/cli/app"
	"github.com/taubyte/tau/pkg/profiling"
)

func main() {
	profiling.Start()

	if err := cli.Run(os.Args...); err != nil {
		log.Fatal(err)
	}
}
