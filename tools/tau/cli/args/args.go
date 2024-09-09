package args

import (
	slices "github.com/taubyte/utils/slices/string"
	"github.com/urfave/cli/v2"
)

func parseCommandArguments(commands []*cli.Command, args ...string) []string {
	if len(args) == 1 || len(commands) == 0 {
		return args
	}
	// We need to also parse sub commands
	for _, cmd := range commands {
		if slices.Contains(args, cmd.Name) {
			// parse arguments after cmd.Name so that `login testprofile -provider github` becomes `login -provider github testprofile`
			// and `login -provider github testprofile` becomes `login -provider github testprofile`
			idx := IndexOf(args, cmd.Name)

			// Check aliases
			if idx == -1 {
				for _, alias := range cmd.Aliases {
					idx = IndexOf(args, alias)
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
					sIdx := IndexOf(args, sCmd.Name)
					if sIdx == -1 {
						for _, alias := range sCmd.Aliases {
							sIdx = IndexOf(args, alias)
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

	// Move commands without the program name
	args = append(args[:1], MovePostFixCommands(args[1:], commands)...)

	validGlobalFlags := ParseFlags(globalFlags)
	argsWithGlobalsMoved := MovePostfixOptions(args, validGlobalFlags)

	var commandStartIdx int

	validCommands := make([]string, len(commands))
	for idx, command := range commands {
		// TODO handle config aliases
		validCommands[idx] = command.Name
	}

	for idx, arg := range argsWithGlobalsMoved[1:] {
		if commandStartIdx != 0 {
			break
		}

		for _, cmd := range validCommands {
			if arg == cmd {
				commandStartIdx = idx + 1 // We do +1 because we start at 1:
				break
			}
		}
	}

	commandArgs := parseCommandArguments(commands, argsWithGlobalsMoved[commandStartIdx:]...)

	return append(argsWithGlobalsMoved[:commandStartIdx], commandArgs...)
}

// Places the command being called at the beginning of the args slice for parsing
func MovePostFixCommands(args []string, commands []*cli.Command) []string {
	// find which command is being called
	cmd_args := []string{}

	for _, cmd := range commands {
		argName := cmd.Name
		idx := IndexOf(args, argName)
		if idx == -1 {
			for _, alias := range cmd.Aliases {
				idx = IndexOf(args, alias)
				if idx != -1 {
					argName = alias
					break
				}
			}
		}
		if idx == -1 {
			continue
		}

		// remove argName from args
		args = append(args[:idx], args[idx+1:]...)

		// add argName to cmd_args
		cmd_args = append(cmd_args, argName)

		if len(cmd.Subcommands) > 0 {
			args = MovePostFixCommands(args, cmd.Subcommands)
		}
	}

	return append(cmd_args, args...)
}
