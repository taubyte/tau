package containers

import (
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/containers/core"
)

var (
	errorBasicFormat     = "%s with: %w"
	errorImageFormat     = "%s for image `%s` with: %w"
	errorContainerFormat = "%s for container Id:`%s` image:`%s` with: %w"

	ErrorImageOptions         = errors.New("image options failed")
	ErrorImageBuild           = errors.New("building image failed")
	ErrorImagePull            = errors.New("pulling image failed")
	ErrorClientPull           = errors.New("client pull failed")
	ErrorImageBuildDockerFile = errors.New("building Dockerfile failed")
	ErrorContainerOptions     = errors.New("container options failed")
	ErrorContainerCreate      = errors.New("creating container failed")
	ErrorContainerStart       = errors.New("start container failed")
	ErrorContainerWait        = errors.New("container wait failed")
	ErrorContainerInspect     = errors.New("inspecting container failed")
	ErrorExitCode             = errors.New("exit-code")
	ErrorContainerLogs        = errors.New("getting container logs failed")
	ErrBackendNotAvailable    = errors.New("backend not available on this platform")
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

func errorContainerOptions(image string, err error) error {
	return fmt.Errorf(errorImageFormat, ErrorContainerOptions, image, err)
}

func errorContainerCreate(image string, err error) error {
	return fmt.Errorf(errorImageFormat, ErrorContainerCreate, image, err)
}

func errorContainerStart(id core.ContainerID, image string, err error) error {
	return fmt.Errorf(errorContainerFormat, ErrorContainerStart, id, image, err)
}

func errorContainerWait(id core.ContainerID, image string, err error) error {
	return fmt.Errorf(errorContainerFormat, ErrorContainerWait, id, image, err)
}

func errorContainerInspect(id core.ContainerID, image string, err error) error {
	return fmt.Errorf(errorContainerFormat, ErrorContainerInspect, id, image, err)
}

func errorContainerExitCode(id core.ContainerID, image string, code int) error {
	return fmt.Errorf("container Id:`%s` image:`%s` failed with %w:%d", id, image, ErrorExitCode, code)
}

func errorContainerLogs(id core.ContainerID, image string, err error) error {
	return fmt.Errorf(errorContainerFormat, ErrorContainerLogs, id, image, err)
}
