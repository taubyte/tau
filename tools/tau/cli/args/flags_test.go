package args_test

import (
	"fmt"
	"testing"

	"github.com/taubyte/tau/tools/tau/cli/args"
	"github.com/urfave/cli/v2"
)

func TestFlags(t *testing.T) {
	testBoolFlag := &cli.BoolFlag{
		Name:    "global",
		Aliases: []string{"g"},
	}
	expectedBoolOptions := []string{"-global", "--global", "-g", "--g"}

	testStringFlag := &cli.StringFlag{
		Name:    "color",
		Aliases: []string{"c"},
	}
	expectedStringOptions := []string{"-color", "--color", "-c", "--c"}

	parsed := args.ParseFlag(testBoolFlag)

	if fmt.Sprintf("%v", parsed.Options) != fmt.Sprintf("%v", expectedBoolOptions) {
		t.Errorf("Expected %v, got %v", expectedBoolOptions, parsed.Options)
	}

	if parsed.IsBoolFlag != true {
		t.Errorf("Expected %v, got %v", true, parsed.IsBoolFlag)
	}

	parsed = args.ParseFlag(testStringFlag)
	if fmt.Sprintf("%v", parsed.Options) != fmt.Sprintf("%v", expectedStringOptions) {
		t.Errorf("Expected %v, got %v", expectedStringOptions, parsed.Options)
	}

	if parsed.IsBoolFlag != false {
		t.Errorf("Expected %v, got %v", false, parsed.IsBoolFlag)
	}

	testFlags := []cli.Flag{testBoolFlag, testStringFlag}
	parsedFlags := args.ParseFlags(testFlags)
	if len(parsedFlags) != len(testFlags) {
		t.Errorf("Expected %d flags, got %d", len(testFlags), len(parsedFlags))
	}

	var foundBoolFlag bool
	var foundStringFlag bool
	for _, flag := range parsedFlags {
		if flag.IsBoolFlag {
			foundBoolFlag = true
			if fmt.Sprintf("%v", flag.Options) != fmt.Sprintf("%v", expectedBoolOptions) {
				t.Errorf("Expected %v, got %v", expectedBoolOptions, flag.Options)
			}
		} else {
			foundStringFlag = true
			if fmt.Sprintf("%v", flag.Options) != fmt.Sprintf("%v", expectedStringOptions) {
				t.Errorf("Expected %v, got %v", expectedStringOptions, flag.Options)
			}
		}
	}

	if !foundBoolFlag {
		t.Errorf("Expected to find a bool flag")
	}

	if !foundStringFlag {
		t.Errorf("Expected to find a string flag")
	}
}
