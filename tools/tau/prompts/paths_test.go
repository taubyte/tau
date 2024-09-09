package prompts_test

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	"github.com/urfave/cli/v2"
)

type pathsTest struct {
	paths []string
}

func (m pathsTest) run(t *testing.T) {
	ctx, err := mock.CLI{
		Flags: []cli.Flag{
			flags.Paths,
		},
		ToSet: map[string]string{
			flags.Paths.Name: strings.Join(m.paths, ","),
		},
	}.Run()
	if err != nil {
		t.Error(err)
		return
	}

	var gotPaths sort.StringSlice = prompts.RequiredPaths(ctx)
	gotPaths.Sort()

	var _paths sort.StringSlice = m.paths
	_paths.Sort()

	if !reflect.DeepEqual(gotPaths, _paths) {
		t.Error(fmt.Errorf("expected %v, got %v", _paths, gotPaths))
	}
}

func TestPaths(t *testing.T) {
	// Set to false if stuck in infinite loop or testing
	prompts.PromptEnabled = true

	pathsTest{
		paths: []string{"/a"},
	}.run(t)

	pathsTest{
		paths: []string{"/help1", "/help2"},
	}.run(t)

}
