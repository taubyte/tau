package env

import (
	"fmt"
	"os"
)

// Get an environment variable and returns an error if not found
func Get(name string) (val string, err error) {
	var ok bool
	if val, ok = os.LookupEnv(name); ok == false {
		return "", fmt.Errorf("Environment variable `%s` not found,", name)
	}

	return
}
