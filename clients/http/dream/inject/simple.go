package inject

import (
	"fmt"

	"github.com/taubyte/tau/dream"
)

func Simple(name string, config *dream.SimpleConfig) Injectable {
	return Injectable{
		Name: name,
		Run: func(universe string) string {
			return fmt.Sprintf("/simple/%s/%s", universe, name)
		},
		Config: config,
		Method: POST,
	}
}
