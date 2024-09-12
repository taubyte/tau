package flags

import (
	"fmt"
	"strings"
)

func UsageOneOfOption(options []string) string {
	return fmt.Sprintf("one of: [%s]", strings.Join(options, ", "))
}
