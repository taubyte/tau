package builder

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	ci "github.com/taubyte/go-simple-container"
	"github.com/taubyte/tau/core/builders"
	specs "github.com/taubyte/tau/pkg/specs/builders"
	"github.com/taubyte/utils/multihash"
)

// setTarball will check if a docker dir exists, and if so tarball the dockerDir
// and set the tarball data in the builder config
func (b *builder) setTarball() error {
	dockerDir := b.wd.DockerDir()
	if _, err := dockerDir.Stat(); err == nil {
		if b.tarball, err = dockerDir.Tar(); err != nil {
			return fmt.Errorf("`%s` found, but failed to create tarball with: %w", dockerDir, err)
		}
	}

	return nil
}

// buildImage returns a container image, if tarball is set then a new image is created
// if not image is attempted to be pulled from dockerhub
func (b *builder) buildImage() (clientImage *ci.Image, err error) {
	environment := b.config.HandleDepreciatedEnvironment()
	image := environment.Image
	ops := []ci.ImageOption{}

	if b.tarball != nil {
		image = fmt.Sprintf("%s-%s", image, strings.ToLower(multihash.Hash(b.tarball)))
		ops = append(ops, ci.Build(bytes.NewReader(b.tarball)))
	}

	return b.containerClient.Image(b.context, image, ops...)
}

// run will initialize and run the container with the given image
func (b *builder) run(output *output, image *ci.Image, environment specs.Environment, ops ...ci.ContainerOption) (err error) {
	output.outDir, err = os.MkdirTemp("/tmp", "*")
	if err != nil {
		return fmt.Errorf("creating temp dir failed with: %w", err)
	}

	// TODO: We should not have to instantiate new containers for each workflow, will need to make slight configurations to go-simple-container as well
	for _, script := range b.config.Workflow {
		ops = append(ops, b.wd.DefaultOptions(script, output.outDir, environment)...)
		container, err := image.Instantiate(b.context, ops...)
		if err != nil {
			return fmt.Errorf("instantiating container failed with: %w", err)
		}

		log, err := container.Run(b.context)
		if err != nil {
			err = fmt.Errorf("running container failed with: %w", err)
		}
		if log != nil {
			if _, _err := output.logs.CopyFrom(log.Combined()); _err != nil {
				_err = fmt.Errorf("copying logs failed with: %w", err)
				if err != nil {
					err = fmt.Errorf("%s:%w", err, _err)
				} else {
					err = _err
				}
			}
		}
		if err != nil {
			return err
		}
	}

	return nil
}

// Close cleans the builder config and closes the container client
func (b *builder) Close() error {
	if err := b.containerClient.Close(); err != nil {
		return fmt.Errorf("closing container client failed with: %w", err)
	}

	return nil
}

// Config returns the taubyte config
func (b *builder) Config() *specs.Config {
	return b.config
}

// Wd returns the builder directory
func (b *builder) Wd() specs.Dir {
	return b.wd
}

// Tarball returns the image tarball set, if any
func (b *builder) Tarball() []byte {
	return b.tarball
}

// Logs returns the builder+container logs
func (o *output) Logs() builders.Logs {
	return o.logs
}

// OutDir returns the built files before compression or zipping
func (o *output) OutDir() string {
	return o.outDir
}

func (l logs) CopyTo(dst io.Writer) (int64, error) {
	if l.File != nil && dst != nil {
		l.File.Seek(0, 0)
		return io.Copy(dst, l.File)
	}

	return 0, errors.New("logs or dst nil")
}

func (l logs) CopyFrom(src io.Reader) (int64, error) {
	if l.File != nil && src != nil {
		return io.Copy(l, src)
	}

	return 0, errors.New("logs or src is nil")
}

func (l logs) FormatErr(format string, args ...any) (formatErr error) {
	formatErr = fmt.Errorf("build failed with: %s", fmt.Sprintf(format, args...))
	if l.File != nil {
		l.File.Seek(0, io.SeekEnd)
		l.File.WriteString(formatErr.Error())
		l.File.Seek(0, io.SeekStart)
	}

	return
}
