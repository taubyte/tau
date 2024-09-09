package args

// TODO "golang.org/x/exp/slices"
func IndexOf(slice []string, val string) int {
	for i, item := range slice {
		if item == val {
			return i
		}
	}

	return -1
}

func buildFlagBoolMap(flags []ParsedFlag) map[string]bool {
	flagBoolMap := map[string]bool{}
	for _, flag := range flags {
		for _, opt := range flag.Options {
			flagBoolMap[opt] = flag.IsBoolFlag
		}
	}

	return flagBoolMap
}
