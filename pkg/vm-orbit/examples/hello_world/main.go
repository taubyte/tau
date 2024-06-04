package main

import "github.com/taubyte/tau/pkg/vm-orbit/satellite"

func main() {
	// methods of helloWorlder will be exported to the module "helloWorld"
	satellite.Export("helloWorld", &helloWorlder{})
}
