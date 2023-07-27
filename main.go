package main

import (
	"log"
	"os"

	cli "github.com/taubyte/tau/cli/app"
)

func main() {
	err := cli.Run(os.Args...)
	if err != nil {
		log.Fatal(err)
	}
}
