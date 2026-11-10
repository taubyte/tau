package args

import (
	"strings"

	"github.com/urfave/cli/v2"
)

func parseCommandArguments(commands []*cli.Command, valued map[string]bool, args ...string) []string {
	if len(args) == 1 || len(commands) == 0 {
		return args
	}
	// We need to also parse sub commands
	if cmd, _, _ := leftmost(args, commands, valued); cmd != nil {
		{
			// parse arguments after cmd.Name so that `login testprofile -provider github` becomes `login -provider github testprofile`
			// and `login -provider github testprofile` becomes `login -provider github testprofile`
			idx := commandIndex(args, cmd.Name, valued)

			// Check aliases
			if idx == -1 {
				for _, alias := range cmd.Aliases {
					idx = commandIndex(args, alias, valued)
					if idx != -1 {
						break
					}
				}
			}

			if idx == -1 {
				return MovePostfixOptions(args, ParseFlags(cmd.Flags))
			}
			args = append(args[:idx], append([]string{cmd.Name}, args[idx+1:]...)...)

			if len(cmd.Subcommands) > 0 {
				for _, sCmd := range cmd.Subcommands {
					sIdx := commandIndex(args, sCmd.Name, valued)
					if sIdx == -1 {
						for _, alias := range sCmd.Aliases {
							sIdx = commandIndex(args, alias, valued)
							if sIdx != -1 {
								break
							}
						}
					}

					if sIdx != -1 {
						subArgs := MovePostfixOptions(args[sIdx:], ParseFlags(sCmd.Flags))
						args = append(args[:sIdx], subArgs...)
					}
				}
			}

			return MovePostfixOptions(args, ParseFlags(cmd.Flags))
		}
	}

	return args
}

func ParseArguments(globalFlags []cli.Flag, commands []*cli.Command, args ...string) []string {
	if len(args) == 1 { // Args can only be >= 1
		return args
	}

	valued := valueFlags(globalFlags, commands)

	// Move commands without the program name
	args = append(args[:1], moveCommands(args[1:], commands, valued)...)

	validGlobalFlags := ParseFlags(globalFlags)
	argsWithGlobalsMoved := MovePostfixOptions(args, validGlobalFlags)

	var commandStartIdx int

	validCommands := make([]string, len(commands))
	for idx, command := range commands {
		// TODO handle config aliases
		validCommands[idx] = command.Name
	}

	for _, cmd := range validCommands {
		// +1 because the program name is not searched
		if idx := commandIndex(argsWithGlobalsMoved[1:], cmd, valued); idx != -1 && (commandStartIdx == 0 || idx+1 < commandStartIdx) {
			commandStartIdx = idx + 1
		}
	}

	commandArgs := parseCommandArguments(commands, valued, argsWithGlobalsMoved[commandStartIdx:]...)

	return append(argsWithGlobalsMoved[:commandStartIdx], commandArgs...)
}

// Places the command being called at the beginning of the args slice for parsing
func MovePostFixCommands(args []string, commands []*cli.Command) []string {
	return moveCommands(args, commands, valueFlags(nil, commands))
}

func moveCommands(args []string, commands []*cli.Command, valued map[string]bool) []string {
	// one command is called per level: the leftmost word naming one, so in
	// `query logs` the top-level `logs` command is not also picked
	cmd, argName, idx := leftmost(args, commands, valued)
	if cmd == nil {
		return args
	}

	// move argName to the front, then its subcommand
	args = append(args[:idx:idx], args[idx+1:]...)
	if len(cmd.Subcommands) > 0 {
		args = moveCommands(args, cmd.Subcommands, valued)
	}
	return append([]string{argName}, args...)
}

// leftmost is the command (and the name or alias used) named first in args.
func leftmost(args []string, commands []*cli.Command, valued map[string]bool) (cmd *cli.Command, argName string, idx int) {
	idx = -1
	for _, c := range commands {
		for _, name := range append([]string{c.Name}, c.Aliases...) {
			if i := commandIndex(args, name, valued); i != -1 && (idx == -1 || i < idx) {
				cmd, argName, idx = c, name, i
			}
		}
	}
	return
}

// valueFlags returns the names of the flags that take a value wherever they
// appear in the command tree. A name that takes a value in one command and none
// in another (push --code, import --code VALUE) is left out: the word after it
// is not known to be its value, and arguments are read as they always were.
func valueFlags(global []cli.Flag, commands []*cli.Command) map[string]bool {
	valued := make(map[string]bool)
	boolean := make(map[string]bool)
	add := func(flags []cli.Flag) {
		for _, f := range flags {
			takes := false
			if d, ok := f.(cli.DocGenerationFlag); ok {
				takes = d.TakesValue()
			}
			for _, name := range f.Names() {
				if takes {
					valued[name] = true
				} else {
					boolean[name] = true
				}
			}
		}
	}
	var walk func([]*cli.Command)
	walk = func(cmds []*cli.Command) {
		for _, c := range cmds {
			add(c.Flags)
			walk(c.Subcommands)
		}
	}
	add(global)
	walk(commands)
	for name := range boolean {
		delete(valued, name)
	}
	return valued
}

// commandIndex finds name among args as a word of its own, so not as the value
// of a flag: in `push project -m cancel`, cancel is a commit message.
func commandIndex(args []string, name string, valued map[string]bool) int {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			flag := strings.TrimLeft(arg, "-")
			if !strings.Contains(flag, "=") && valued[flag] {
				i++
			}
			continue
		}
		if arg == name {
			return i
		}
	}
	return -1
}
