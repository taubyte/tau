package project

import (
	"errors"
	"flag"
	"sync"
	"testing"
	"time"

	"github.com/taubyte/tau/pkg/git"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

type fakeRepos struct{ config, code *git.Repository }

func (f fakeRepos) Config() (*git.Repository, error) { return f.config, nil }
func (f fakeRepos) Code() (*git.Repository, error)   { return f.code, nil }
func (f fakeRepos) CurrentBranch() (string, error)   { return "main", nil }

// Config is pushed and done before code starts; a failed config stops there.
func TestDualRepoConfigBeforeCode(t *testing.T) {
	repos := fakeRepos{config: &git.Repository{}, code: &git.Repository{}}
	ctx := cli.NewContext(cli.NewApp(), flag.NewFlagSet("t", flag.ContinueOnError), nil)

	run := func(configErr error) ([]string, error) {
		var (
			mu    sync.Mutex
			order []string
		)
		record := func(e string) { mu.Lock(); order = append(order, e); mu.Unlock() }
		err := (&dualRepoHandler{
			ctx: ctx, repository: repos, projectName: "p",
			errorFormat: func(p string) error { return errors.New("project " + p + " failed") },
			action: func(r *git.Repository) error {
				name := "code"
				if r == repos.config {
					name = "config"
				}
				record(name + " start")
				time.Sleep(5 * time.Millisecond)
				record(name + " end")
				if name == "config" {
					return configErr
				}
				return nil
			},
		}).Run()
		return order, err
	}

	order, err := run(nil)
	assert.NilError(t, err)
	assert.DeepEqual(t, order, []string{"config start", "config end", "code start", "code end"})

	boom := errors.New("boom")
	order, err = run(boom)
	assert.DeepEqual(t, order, []string{"config start", "config end"})
	assert.ErrorIs(t, err, boom)
	assert.ErrorContains(t, err, "project p failed")
}
