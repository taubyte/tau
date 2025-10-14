package containers

import (
	"errors"
	"fmt"
)

var errorBasicFormat = "%s with: %w"

// Image Method Errors
var (
	ErrorImageOptions         = errors.New("image options failed")
	ErrorImageBuild           = errors.New("building image failed")
	ErrorImagePull            = errors.New("pulling image failed")
	ErrorClientPull           = errors.New("client pull failed")
	ErrorImageBuildDockerFile = errors.New("building Dockerfile failed")
	ErrorImageBuildResCopy    = errors.New("copying response from image build failed")
	ErrorImagePullStatus      = errors.New("copying pull status failed")

	ErrorContainerOptions = errors.New("container options failed")
	ErrorContainerCreate  = errors.New("creating container failed")

	errorImageFormat = "%s for image `%s` with: %w"
)

func errorImageOptions(image string, err error) error {
	return fmt.Errorf(errorImageFormat, ErrorImageOptions, image, err)
}

func errorImageBuild(image string, err error) error {
	return fmt.Errorf(errorImageFormat, ErrorImageBuild, image, err)
}

func errorImagePull(image string, err error) error {
	return fmt.Errorf(errorImageFormat, ErrorImagePull, image, err)
}

func errorClientPull(err error) error {
	return fmt.Errorf(errorBasicFormat, ErrorClientPull, err)
}

func errorImageBuildDockerFile(err error) error {
	return fmt.Errorf(errorBasicFormat, ErrorImageBuildDockerFile, err)
}

func errorImageBuildResCopy(err error) error {
	return fmt.Errorf(errorBasicFormat, ErrorImageBuildResCopy, err)
}

func errorImagePullStatus(err error) error {
	return fmt.Errorf(errorBasicFormat, ErrorImagePullStatus, err)
}

func errorContainerOptions(image string, err error) error {
	return fmt.Errorf(errorImageFormat, ErrorContainerOptions, image, err)
}

func errorContainerCreate(image string, err error) error {
	return fmt.Errorf(errorImageFormat, ErrorContainerCreate, image, err)
}

// Container Method Errors
var (
	ErrorContainerStart   = errors.New("start container failed")
	ErrorContainerWait    = errors.New("container wait failed")
	ErrorClientWait       = errors.New("client wait failed")
	ErrorContainerInspect = errors.New("inspecting container failed")
	ErrorExitCode         = errors.New("exit-code")
	ErrorContainerLogs    = errors.New("getting container logs failed")

	errorContainerFormat = "%s for container Id:`%s` image:`%s` with: %w"
)

func errorContainerStart(id, image string, err error) error {
	return fmt.Errorf(errorContainerFormat, ErrorContainerStart, id, image, err)
}

func errorContainerWait(id, image string, err error) error {
	return fmt.Errorf(errorContainerFormat, ErrorContainerWait, id, image, err)
}

func errorContainerInspect(id, image string, err error) error {
	return fmt.Errorf(errorContainerFormat, ErrorContainerInspect, id, image, err)
}

func errorContainerExitCode(id, image string, code int) error {
	return fmt.Errorf("container Id:`%s` image:`%s` failed with %w:%d", id, image, ErrorExitCode, code)
}

func errorContainerLogs(id, image string, err error) error {
	return fmt.Errorf(errorContainerFormat, ErrorContainerLogs, id, image, err)
}
