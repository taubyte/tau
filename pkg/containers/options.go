package containers

import (
	"bytes"
	"io"

	"github.com/taubyte/tau/utils/bundle"
)

/**************** Image Options ****************/

// ImageOption is a function to set configuration to the Image object.
type ImageOption func(*DockerImage) error

// Build returns an ImageOption to build a tarball of a Dockerfile
func Build(tarball io.Reader) ImageOption {
	return func(i *DockerImage) error {
		i.buildTarball = tarball
		return nil
	}
}

func Dockerfile(dockerfile string) ImageOption {
	return func(i *DockerImage) error {
		// create a tar of the dockerfile in memory
		var buf bytes.Buffer
		err := bundle.SingleFileTarball(dockerfile, &buf)
		if err != nil {
			return err
		}

		i.buildTarball = &buf
		return nil
	}
}

func Output(output io.Writer) ImageOption {
	return func(i *DockerImage) error {
		i.output = output
		return nil
	}
}

/**************** Container Options ****************/

// ContainerOption is a function to set configuration to the Container object.
type ContainerOption func(*Container) error

// WorkDir sets the working directory of the container, where calls will be made.
func WorkDir(workDir string) ContainerOption {
	return func(c *Container) error {
		c.workDir = workDir
		return nil
	}
}

// Shell sets the shell-form of RUN, CMD, ENTRYPOINT
func Shell(cmd []string) ContainerOption {
	return func(c *Container) error {
		c.shell = cmd
		return nil
	}
}

// Command sets the commands to be run by the container after being built.
func Command(cmd []string) ContainerOption {
	return func(c *Container) error {
		c.cmd = cmd
		return nil
	}
}

// Volume sets local directories to be volumed in the container.
func Volume(sourcePath, containerPath string) ContainerOption {
	return func(c *Container) error {
		c.volumes = append(c.volumes, volume{
			source: sourcePath,
			target: containerPath,
		},
		)
		return nil
	}
}

// Variable sets an environment variable in the container.
func Variable(key, value string) ContainerOption {
	return func(c *Container) error {
		c.env = append(c.env, toEnvFormat(key, value))
		return nil
	}
}

// Variables sets multiple environment variables in the container.
func Variables(vars map[string]string) ContainerOption {
	return func(c *Container) error {
		for key, value := range vars {
			c.env = append(c.env, toEnvFormat(key, value))
		}
		return nil
	}
}

func toEnvFormat(key, value string) string {
	return key + "=" + value
}
