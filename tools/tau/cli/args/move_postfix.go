package args

import (
	"strings"
)

/*
Used to move non option(-) arguments to the end of the command,  this way
positional arguments do not have to go before the optional arguments

Arguments do not behave how a unix cli would expect them to:
  - https://github.com/urfave/cli/issues/427
*/
func MovePostfixOptions(args []string, validFlags []ParsedFlag) []string {
	if len(args) == 1 {
		return args
	}

	// Create new arguments with the first arg as the command
	new_args := []string{args[0]}

	// Arguments after the command
	args = args[1:]
	positional_args := make([]string, 0)
	flagBoolMap := buildFlagBoolMap(validFlags)

	// skip handles the case of `--name someName` vs there is no skip needed for `--name=someName`
	var skip bool

	for idx, arg := range args {
		if skip {
			skip = false
			continue
		}

		if len(arg) == 0 {
			new_args = append(new_args, arg)
			continue
		}

		if arg[:1] == "-" {
			flagIsBool, ok := flagBoolMap[arg]
			if ok {
				if flagIsBool || strings.Contains(arg, "=") || len(args) == idx+1 {
					if flagIsBool {

						// if next idx exists and strings.ToLower(next idx) == "true" || "false" remove and skip
						if len(args) > idx+1 {
							if strings.ToLower(args[idx+1]) == "true" {
								skip = true
							} else if strings.ToLower(args[idx+1]) == "false" {
								skip = true
								arg = "-no-" + strings.ReplaceAll(arg, "-", "")
							}
						}
					}

					new_args = append(new_args, arg)
					continue
				}

				// `--name someName`
				new_args = append(new_args, arg, args[idx+1])
				skip = true
				continue
			}
		}

		// Everything else
		positional_args = append(positional_args, arg)
	}

	return append(new_args, positional_args...)
}
