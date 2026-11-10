package jobs

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/core/services/tns"
	"gotest.tools/v3/assert"
)

type configTns struct{ tns.Client }

func (configTns) Fetch(tns.Path) (tns.Object, error) { return configObject{}, nil }

type configObject struct{ tns.Object }

func (configObject) Interface() interface{} {
	return map[string]interface{}{"ssh": "file:///nonexistent"}
}

// The config clone is shared by every job of a project and a build can be long,
// so a code job gives the clone back as soon as it has read the config.
func TestCodeJobGivesBackTheConfigClone(t *testing.T) {
	gitCacheRoot = t.TempDir()
	c := Context{ctx: context.Background(), ProjectID: "projA", ConfigRepoId: 1, Tns: configTns{}}
	c.Job = &patrick.Job{}
	c.LogFile, _ = os.CreateTemp(t.TempDir(), "log")
	assert.Assert(t, (&code{c}).handle() != nil) // the clone of the config fails

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, unlock, err := lockRepoDir(ctx, "projA", "1")
	assert.NilError(t, err)
	unlock()
}
