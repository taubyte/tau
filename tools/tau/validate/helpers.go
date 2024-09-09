package validate

import (
	"errors"
	"regexp"
)

func MatchAllString(val string, expressions [][]string) error {
	return matchAllString(val, expressions)
}

func matchAllString(val string, expressions [][]string) error {
	// TODO use a struct for the regext to make it human readable vs exp[0/1]
	var match bool
	for _, exp := range expressions {
		match, _ = regexp.MatchString(exp[1], val)
		if !match {
			return errors.New(exp[0])
		}
	}
	return nil
}

func InList(value string, values []string) bool {
	if len(value) == 0 {
		return true
	}

	for _, v := range values {
		if v == value {
			return true
		}
	}

	return false
}
