package main

import (
	"log"
	"os"

	"bitbucket.org/taubyte/odo/cli"
)

func main() {
	err := cli.Start(os.Args...)
	if err != nil {
		log.Fatal(err)
	}
}
