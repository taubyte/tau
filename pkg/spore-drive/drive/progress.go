package drive

import (
	"fmt"
	"path"

	host "github.com/taubyte/tau/pkg/mycelium/host"
	"github.com/taubyte/tau/pkg/spore-drive/course"
)

type Progress interface {
	Path() string // example: /hypha-generated-name/host/stepId
	Name() string
	Progress() int
	Error() error
	String() string
}

type progress struct {
	hypha    *course.Hypha
	host     host.Host
	stepName string
	progress int //percentage
	err      error
}

func (p *progress) Path() string { // example: /hypha-generated-name/host/stepId
	return "/" + path.Join(p.hypha.Name, p.host.String(), p.stepName)
}

func (p *progress) Name() string {
	return p.stepName
}

func (p *progress) Progress() int {
	return p.progress
}

func (p *progress) Error() error {
	return p.err
}

func (p *progress) String() string {
	if p.err == nil {
		return fmt.Sprintf("[%s][%s] %s %d", p.hypha.Name, p.host.Name(), p.stepName, p.progress)
	}
	return fmt.Sprintf("[%s][%s] %s %s", p.hypha.Name, p.host.Name(), p.stepName, p.err.Error())
}
