package main

import (
	"log"
	"os"

	cli "github.com/taubyte/odo/cli/commands"
)

func main() {
	err := cli.Start(os.Args...)
	if err != nil {
		log.Fatal(err)
	}
}
