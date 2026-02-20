//go:build docker_integration

package containers_test

import (
	"bytes"
	"context"
	"fmt"

	containers "github.com/taubyte/tau/pkg/containers"
)

var client *containers.Client
var err error
var image *containers.DockerImage

func ExampleNew() {
	// create new docker client
	client, err = containers.New()
	if err != nil {
		return
	}

	fmt.Println("success")
	// Output: success
}

func ExampleClient_Image() {
	// create new docker client
	client, err := containers.New()
	if err != nil {
		return
	}

	// declare new docker image `node` from docker hub public image `node`
	image, err = client.Image(context.Background(), "node")
	if err != nil {
		return
	}

	dockerFileTarBall := bytes.NewBuffer(nil)

	// Build a custom image using a Dockerfile tarball.
	// This will error because we are sending nil bytes. Refer to README for how to build this tarball.
	image, err = client.Image(context.Background(), "custom/test:version1", containers.Build(dockerFileTarBall))
	if err == nil {
		return
	}

	fmt.Println("success")
	// Output: success
}
