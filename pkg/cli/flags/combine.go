package flags

import (
	"fmt"

	"github.com/urfave/cli/v2"
)

func Combine(flags ...interface{}) []cli.Flag {
	var combined []cli.Flag

	for _, flagIface := range flags {
		switch flag := flagIface.(type) {
		case []cli.Flag:
			combined = append(combined, flag...)
		case cli.Flag:
			combined = append(combined, flag)
		default:
			panic(fmt.Sprintf("unknown flag type: %T", flag))
		}
	}

	return combined
}
