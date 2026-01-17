package builders

import (
	"io"

	ci "github.com/taubyte/tau/pkg/containers"
	"github.com/taubyte/tau/pkg/specs/builders"
)

type Builder interface {
	// Build will build the given working directory as per builder configuration and returns Output
	Build(...ci.ContainerOption) (Output, error)
	// Close cleans up the builder
	Close() error
	// Config returns the builder configuration
	Config() *builders.Config
	// Wd returns the builder working directory
	Wd() builders.Dir
	// Tarball returns the tarball of the image used to build, if any
	Tarball() []byte
}

type Output interface {
	// Compress takes a supported CompressionMethod, compress the files built by the Builder, and returns the ReadSeekCloser of the compressed file
	Compress(CompressionMethod) (io.ReadSeekCloser, error)
	// OutDir returns the output directory of the built files, pre zip or compress
	OutDir() string
}

// CompressionMethod defines the method used to compress build Output of a Builder
type CompressionMethod uint32
