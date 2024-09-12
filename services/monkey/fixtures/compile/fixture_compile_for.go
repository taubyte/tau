package compile

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/taubyte/tau/dream"
	tauTemplates "github.com/taubyte/tau/pkg/cli/singletons/templates"
	spec "github.com/taubyte/tau/pkg/specs/common"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func init() {
	dream.RegisterFixture("compileFor", CompileFor)
}

type BasicCompileFor struct {
	ProjectId     string
	ApplicationId string
	ResourceId    string
	Branch        string
	Paths         []string
	Call          string
}

func (b *BasicCompileFor) parse(params []interface{}) error {
	basic, ok := params[0].(BasicCompileFor)
	if ok {
		*b = basic

		if b.Branch == "" {
			b.Branch = spec.DefaultBranches[0]
		}

		if len(b.Paths) == 0 {
			return errors.New("path is required")
		}

		return nil
	}

	if len(params) < 6 {
		return fmt.Errorf("expected 6 parameters got %d", len(params))
	}

	b.ProjectId, ok = params[0].(string)
	if !ok || b.ProjectId == "" {
		return errors.New("ProjectId is required")
	}

	b.ApplicationId = params[1].(string)
	if !ok {
		return errors.New("ApplicationId is required at least to be empty")
	}

	b.ResourceId = params[2].(string)
	if !ok || b.ResourceId == "" {
		return errors.New("ResourceId is required")
	}

	b.Branch, ok = params[3].(string)
	if !ok || b.Branch == "" {
		b.Branch = spec.DefaultBranches[0]
	}

	b.Call, ok = params[5].(string)
	if !ok {
		b.Call = ""
	}

	path, _ := params[4].(string)
	if len(path) < 1 {
		return errors.New("path is required")
	}

	b.Paths = strings.Split(path, ",")

	return nil
}

/*  Example
dream inject compileFor \
	--pid QmZY4u91d1YALDN2LTbpVtgwW8iT5cK9PE1bHZqX9J51Tv \
	--res QmTLqapULGiA5ZFWk2ckEjKq2B6kGVuFUF1frSAeeGjuGt \
	-b master \
	-c ping \
	--path '/home/sam/Downloads/bafybeibnkp6wrfjcepptbnxbxy6j7l3rcgxjwur5tr7crmjj5pt5a4lca4.wasm,/home/sam/Downloads/bafybeibnkp6wrfjcepptbnxbxy6j7l3rcgxjwur5tr7crmjj5pt5a4lca4.wasm'
*/

func CompileFor(u *dream.Universe, params ...interface{}) error {
	simple, err := u.Simple("client")
	if err != nil {
		return fmt.Errorf("failed getting simple with error: %v", err)
	}

	err = simple.Provides("tns", "hoarder")
	if err != nil {
		return err
	}

	err = u.Provides(
		"hoarder",
		"tns",
	)
	if err != nil {
		return err
	}

	b := &BasicCompileFor{}
	err = b.parse(params)
	if err != nil {
		return err
	}

	if len(b.ApplicationId) > 0 {
		return errors.New("application not implemented for this fixture")
	}

	for _, _path := range b.Paths {
		if !path.IsAbs(_path) {
			return fmt.Errorf("path must be absolute got %s", b.Paths)
		}
	}

	hoarder, err := simple.Hoarder()
	if err != nil {
		return err
	}

	ctx := resourceContext{
		universe:      u,
		simple:        simple,
		projectId:     b.ProjectId,
		applicationId: b.ApplicationId,
		resourceId:    b.ResourceId,
		branch:        b.Branch,
		paths:         b.Paths,
		call:          b.Call,
		templateRepo:  tauTemplates.Repository(),
		hoarderClient: hoarder,
	}

	resourceIface, err := ctx.get()
	if err != nil {
		return err
	}

	switch resource := resourceIface.(type) {
	case *structureSpec.Function:
		err = ctx.function(resource)
	case *structureSpec.SmartOp:
		err = ctx.smartops(resource)
	case *structureSpec.Library:
		err = ctx.library(resource)
	case *structureSpec.Website:
		err = ctx.website(resource)
	default:
		return fmt.Errorf("resource not found in: %s", ctx.display())
	}

	return err
}
