package website

import (
	"path"

	"github.com/taubyte/tau/legacy/specs/builders"
	ci "github.com/taubyte/tau/pkg/containers"
)

func (d Dir) BuildZip() string {
	return path.Join(d.String(), ZipFile)
}

func (d Dir) SetWorkDir() ci.ContainerOption {
	return ci.WorkDir("/" + builders.Source)
}
