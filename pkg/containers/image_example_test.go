package containers_test

import (
	"context"
	"fmt"

	containers "github.com/taubyte/tau/pkg/containers"
)

var container *containers.Container

func ExampleDockerImage_Instantiate() {
	// create new docker client
	client, err := containers.New()
	if err != nil {
		return
	}

	ctx := context.Background()

	// declare new docker image `node` from docker hub public image `node`
	image, err := client.Image(ctx, "node")
	if err != nil {
		return
	}

	// declare container options to set environment variable, and command to be run by container
	options := []containers.ContainerOption{
		containers.Variable("KEY", "value"),
		containers.Command([]string{"echo", "Hello World"}),
	}

	// create container from the image
	container, err = image.Instantiate(ctx, options...)
	if err != nil {
		return
	}

	fmt.Println("success")
	// Output: success
}
