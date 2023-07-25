package hoarder

import (
	"fmt"
	"regexp"
)

func checkMatch(regex bool, match, toMatch, name string) error {
	// Check that the match from config works
	if regex {
		matched, err := regexp.Match(toMatch, []byte(match))
		if err != nil {
			return fmt.Errorf("matching regex `%s` with `%s` failed with: %s", match, toMatch, err)
		}

		if !matched {
			return fmt.Errorf("`%s` did not match regex `%s` from config `%s`", match, toMatch, name)
		}
	} else if !regex {
		if match != toMatch {
			return fmt.Errorf("`%s` did not match string `%s` from config `%s`", match, toMatch, name)
		}
	}

	return nil
}
