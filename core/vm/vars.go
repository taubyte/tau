package vm

import "time"

var (
	GetTimeout = 60 * time.Second

	//TODO: Lookup should handle timeout (tns fetch may need to take context)
	LookupTimeout = 10 * time.Second
)
