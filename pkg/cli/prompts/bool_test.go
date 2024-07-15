package prompts_test

import (
	"strings"
	"testing"

	"github.com/taubyte/tau/pkg/cli/flags"
	"github.com/taubyte/tau/pkg/cli/prompts"
	"github.com/taubyte/tau/pkg/cli/prompts/mock"
	"github.com/urfave/cli/v2"
)

type boolTest struct {
	value string
}

func getTestFlag() *flags.BoolWithInverseFlag {
	return &flags.BoolWithInverseFlag{
		BoolFlag: &cli.BoolFlag{
			Name: "doThing",
		},
	}
}

func (m boolTest) run(t *testing.T) {
	var arg string
	if m.value == "false" {
		arg = "--no-doThing"
	} else {
		arg = "--doThing"
	}

	ctx, err := mock.CLI{
		Flags: flags.Combine(getTestFlag()),
	}.Run(arg)
	if err != nil {
		t.Error(err)
		return
	}

	value := prompts.GetOrAskForBool(ctx, getTestFlag().Name, "")
	if value != (strings.ToLower(m.value) == "true") {
		t.Errorf("expected %s, got %v", m.value, value)
	}
}

func TestBool(t *testing.T) {
	// Set to false if stuck in infinite loop or testing
	prompts.PromptEnabled = true

	boolTest{
		value: "true",
	}.run(t)

	boolTest{
		value: "false",
	}.run(t)
}
