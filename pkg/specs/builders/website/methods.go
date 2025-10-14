package website

import (
	"path"

	ci "github.com/taubyte/tau/pkg/containers"
	"github.com/taubyte/tau/pkg/specs/builders"
)

func (d Dir) BuildZip() string {
	return path.Join(d.String(), ZipFile)
}

func (d Dir) SetWorkDir() ci.ContainerOption {
	return ci.WorkDir("/" + builders.Source)
}
