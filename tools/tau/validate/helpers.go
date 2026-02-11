package validate

import (
	"errors"
	"fmt"
	"regexp"
)

func MatchAllString(val string, expressions [][]string) error {
	return matchAllString(val, expressions)
}

func matchAllString(val string, expressions [][]string) error {
	for _, exp := range expressions {
		if len(exp) < 2 {
			return fmt.Errorf("invalid expression: expected [message, regex], got %d elements", len(exp))
		}
		match, err := regexp.MatchString(exp[1], val)
		if err != nil {
			return fmt.Errorf("invalid regex %q: %w", exp[1], err)
		}
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
