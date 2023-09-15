package compile

import (
	"fmt"

	"github.com/taubyte/go-interfaces/services/hoarder"
	"github.com/taubyte/go-interfaces/services/monkey"
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
	hoarderClient hoarder.Client
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
	tns, err := c.simple.TNS()
	if err != nil {
		return nil, err
	}

	resource, err = tns.Function().Relative(c.projectId, c.applicationId, c.branch).GetById(c.resourceId)
	if err == nil {
		return
	}

	resource, err = tns.Library().Relative(c.projectId, c.applicationId, c.branch).GetById(c.resourceId)
	if err == nil {
		return
	}

	resource, err = tns.Website().Relative(c.projectId, c.applicationId, c.branch).GetById(c.resourceId)
	if err == nil {
		return
	}

	resource, err = tns.SmartOp().Relative(c.projectId, c.applicationId, c.branch).GetById(c.resourceId)
	if err == nil {
		return
	}

	return
}

type fakeMonkey struct {
	monkey.Service
	hoarderClient hoarder.Client
}

func (f fakeMonkey) Hoarder() hoarder.Client {
	return f.hoarderClient
}
