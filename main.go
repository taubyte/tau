package main

import (
	"log"
	"os"

	cli "github.com/taubyte/tau/cli/app"
)

func main() {
	if err := cli.Run(os.Args...); err != nil {
		log.Fatal(err)
	}
}
