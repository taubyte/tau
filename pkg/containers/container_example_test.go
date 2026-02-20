//go:build docker_integration

package containers_test

import (
	"bytes"
	"context"
	"fmt"

	containers "github.com/taubyte/tau/pkg/containers"
)

func ExampleContainer_Run() {
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
	container, err := image.Instantiate(ctx, options...)
	if err != nil {
		return
	}

	// runs the container
	logs, err := container.Run(ctx)
	if err != nil {
		return
	}

	var buf bytes.Buffer

	// read logs from the ran container
	buf.ReadFrom(logs.Combined())

	fmt.Println(buf.String())
	// Output: Hello World
}
