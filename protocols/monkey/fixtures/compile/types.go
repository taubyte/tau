package compile

import (
	"fmt"

	git "github.com/taubyte/go-simple-git"
	"github.com/taubyte/tau/libdream"
)

type resourceContext struct {
	universe      *libdream.Universe
	simple        *libdream.Simple
	projectId     string
	applicationId string
	resourceId    string
	branch        string
	paths         []string
	call          string
	templateRepo  *git.Repository
}

func (c resourceContext) display() string {
	return fmt.Sprint(
		fmt.Sprint("Project:", c.projectId),
		fmt.Sprint("Application:", c.applicationId),
		fmt.Sprint("Branch:", c.branch),
		fmt.Sprint("ResourceID:", c.resourceId),
	)
}

func (c resourceContext) get() (resource interface{}, err error) {
	resource, err = c.simple.TNS().Function().Relative(c.projectId, c.applicationId, c.branch).GetById(c.resourceId)
	if err == nil {
		return
	}

	resource, err = c.simple.TNS().Library().Relative(c.projectId, c.applicationId, c.branch).GetById(c.resourceId)
	if err == nil {
		return
	}

	resource, err = c.simple.TNS().Website().Relative(c.projectId, c.applicationId, c.branch).GetById(c.resourceId)
	if err == nil {
		return
	}

	resource, err = c.simple.TNS().SmartOp().Relative(c.projectId, c.applicationId, c.branch).GetById(c.resourceId)
	if err == nil {
		return
	}

	return
}
