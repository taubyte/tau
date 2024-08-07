package compile

import (
	"errors"
	"fmt"
	"io"

	"github.com/pterm/pterm"
	"github.com/taubyte/tau/pkg/specs/methods"
	"github.com/taubyte/tau/services/monkey/jobs"
)

func (ctx resourceContext) stashAndPush(id string, file io.ReadSeekCloser) error {
	if file == nil {
		return errors.New("file is nil")
	}

	tnsClient, err := ctx.simple.TNS()
	if err != nil {
		return err
	}

	c := jobs.Context{
		Tns:  tnsClient,
		Node: ctx.universe.TNS().Node(),
		Monkey: fakeMonkey{
			hoarderClient: ctx.hoarderClient,
		},
		GeneratedDomainRegExp: generatedDomainRegExp,
	}
	c.ForceContext(ctx.universe.Context())

	cid, err := c.StashBuildFile(file)
	if err != nil {
		return fmt.Errorf("stash failed with: %s", err)
	}

	assetKey, err := methods.GetTNSAssetPath(ctx.projectId, id, ctx.branch)
	if err != nil {
		return err
	}

	pterm.Info.Printf("Stashing %s asset as %s\n", assetKey.String(), cid)

	err = c.Tns.Push(assetKey.Slice(), cid)
	if err != nil {
		return fmt.Errorf("saving asset file failed with")
	}

	return nil
}
