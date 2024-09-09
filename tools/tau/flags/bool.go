package flags

import (
	"bytes"
	"flag"
	"fmt"
	"regexp"
	"strings"

	"github.com/urfave/cli/v2"
)

var (
	DefaultInverseBoolPrefix = "no-"
)

type BoolWithInverseFlag struct {
	// The BoolFlag which the positive and negative flags are generated from
	*cli.BoolFlag

	// The prefix used to indicate a negative value
	// Default: `env` becomes `no-env`
	InversePrefix string

	positiveFlag *cli.BoolFlag
	negativeFlag *cli.BoolFlag

	// pointers obtained from the embedded bool flag
	posDest  *bool
	posCount *int

	negDest *bool
}

func (s *BoolWithInverseFlag) Flags() []cli.Flag {
	return []cli.Flag{s.positiveFlag, s.negativeFlag}
}

func (s *BoolWithInverseFlag) IsSet() bool {
	return (*s.posCount > 0) || (s.positiveFlag.IsSet() || s.negativeFlag.IsSet())
}

func (s *BoolWithInverseFlag) Value() bool {
	return *s.posDest
}

func (s *BoolWithInverseFlag) RunAction(ctx *cli.Context) error {
	if *s.negDest && *s.posDest {
		return fmt.Errorf("cannot set both flags `--%s` and `--%s`", s.positiveFlag.Name, s.negativeFlag.Name)
	}

	if *s.negDest {
		err := ctx.Set(s.positiveFlag.Name, "false")
		if err != nil {
			return err
		}
	}

	if s.BoolFlag.Action != nil {
		return s.BoolFlag.Action(ctx, s.Value())
	}

	return nil
}

/*
initialize creates a new BoolFlag that has an inverse flag

consider a bool flag `--env`, there is no way to set it to false
this function allows you to set `--env` or `--no-env` and in the command action
it can be determined that BoolWithInverseFlag.IsSet()
*/
func (parent *BoolWithInverseFlag) initialize() {
	child := parent.BoolFlag

	parent.negDest = new(bool)
	if child.Destination != nil {
		parent.posDest = child.Destination
	} else {
		parent.posDest = new(bool)
	}

	if child.Count != nil {
		parent.posCount = child.Count
	} else {
		parent.posCount = new(int)
	}

	parent.positiveFlag = child
	parent.positiveFlag.Destination = parent.posDest
	parent.positiveFlag.Count = parent.posCount

	parent.negativeFlag = &cli.BoolFlag{
		Category:    child.Category,
		DefaultText: child.DefaultText,
		FilePath:    child.FilePath,
		Required:    child.Required,
		Hidden:      child.Hidden,
		HasBeenSet:  child.HasBeenSet,
		Value:       child.Value,
		Destination: parent.negDest,
	}

	// Set inverse names ex: --env => --no-env
	parent.negativeFlag.Name = parent.inverseName()
	parent.negativeFlag.Aliases = parent.inverseAliases()

	if len(child.EnvVars) > 0 {
		parent.negativeFlag.EnvVars = make([]string, len(child.EnvVars))
		for idx, envVar := range child.EnvVars {
			parent.negativeFlag.EnvVars[idx] = strings.ToUpper(parent.InversePrefix) + envVar
		}
	}
}

func (parent *BoolWithInverseFlag) inverseName() string {
	if parent.InversePrefix == "" {
		parent.InversePrefix = DefaultInverseBoolPrefix
	}

	return parent.InversePrefix + parent.BoolFlag.Name
}

func (parent *BoolWithInverseFlag) inverseAliases() (aliases []string) {
	if len(parent.BoolFlag.Aliases) > 0 {
		aliases = make([]string, len(parent.BoolFlag.Aliases))
		for idx, alias := range parent.BoolFlag.Aliases {
			aliases[idx] = parent.InversePrefix + alias
		}
	}

	return
}

func (s *BoolWithInverseFlag) Apply(set *flag.FlagSet) error {
	if s.positiveFlag == nil {
		s.initialize()
	}

	if err := s.positiveFlag.Apply(set); err != nil {
		return err
	}

	if err := s.negativeFlag.Apply(set); err != nil {
		return err
	}

	return nil
}

func (s *BoolWithInverseFlag) Names() []string {
	// Get Names when flag has not been initialized
	if s.positiveFlag == nil {
		return append(s.BoolFlag.Names(), cli.FlagNames(s.inverseName(), s.inverseAliases())...)
	}

	if *s.negDest {
		return s.negativeFlag.Names()
	}

	if *s.posDest {
		return s.positiveFlag.Names()
	}

	return append(s.negativeFlag.Names(), s.positiveFlag.Names()...)
}

// Example for BoolFlag{Name: "env"}
// --env     | --no-env    Usage...
func (s *BoolWithInverseFlag) String() string {
	// Initialize the flag if String is called before cli is initialized
	if s.positiveFlag == nil {
		s.initialize()
	}

	// Replace HorizontalTab with a space so the flags stick
	posFlagString := string(bytes.Replace([]byte(s.positiveFlag.String()), []byte{9}, []byte{32}, 1))

	// Inject a right parenthesis before the HorizontalTab
	negFlagString := string(bytes.Replace([]byte(s.negativeFlag.String()), []byte{9}, []byte{41, 9}, 1))

	// Format
	var ret string
	if len(s.positiveFlag.Usage) > 0 {

		// Remove Usage from positive flag string
		posFlagString = strings.Replace(posFlagString, s.positiveFlag.Usage+" ", "", 1)

		ret = fmt.Sprintf("%s(disable: %s%s", posFlagString, negFlagString, s.positiveFlag.Usage)
	} else {
		ret = fmt.Sprintf("%s(disable: %s", posFlagString, negFlagString)
	}

	// Remove (default: false)
	return regexp.MustCompile(`\(default:.*?false\)`).ReplaceAllLiteralString(ret, "")
}
