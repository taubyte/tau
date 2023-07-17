package main

import (
	"os"

	moody "github.com/taubyte/go-interfaces/moody"
)

func main() {

	initLogger()
	initContext()

	if err := defineCLI().Run(os.Args); err != nil {
		Logger.Error(moody.Object{"message": err.Error()})
	}

	os.Exit(0)
}
