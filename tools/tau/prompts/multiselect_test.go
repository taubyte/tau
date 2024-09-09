package prompts_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	"github.com/urfave/cli/v2"
)

type multiSelectTest struct {
	options  []string
	selected string
}

var multiselectFlag = &cli.StringSliceFlag{
	Name: "fruits",
}

func (m multiSelectTest) run(t *testing.T) {
	ctx, err := mock.CLI{
		Flags: []cli.Flag{
			multiselectFlag,
		},
		ToSet: map[string]string{
			multiselectFlag.Name: m.selected,
		},
	}.Run()
	if err != nil {
		t.Error(err)
		return
	}

	cnf := prompts.MultiSelectConfig{
		Field:   multiselectFlag.Name,
		Prompt:  "",
		Options: m.options,
	}
	gotSlice := prompts.MultiSelect(ctx, cnf)

	// sort got and expected then use reflect to compare equivalency
	var _got sort.StringSlice = gotSlice
	_got.Sort()
	var _expected sort.StringSlice = strings.Split(m.selected, ",")
	_expected.Sort()

	if fmt.Sprintf("%v", _got) != fmt.Sprintf("%v", _expected) {
		t.Error(fmt.Errorf("expected %v, got %v", _expected, _got))
	}
}

func TestMultiSelect(t *testing.T) {
	// Set to false if stuck in infinite loop or testing
	prompts.PromptEnabled = false

	multiSelectTest{
		options:  []string{"a", "b", "c"},
		selected: "a,b",
	}.run(t)

	multiSelectTest{
		options:  []string{"a", "b", "c"},
		selected: "a,c",
	}.run(t)

	multiSelectTest{
		options:  []string{"a", "b", "c"},
		selected: "b",
	}.run(t)
}
