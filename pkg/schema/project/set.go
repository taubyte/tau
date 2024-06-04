package project

import (
	"github.com/taubyte/tau/pkg/schema/basic"
)

func Id(value string) basic.Op {
	return basic.Set("id", value)
}

func Name(value string) basic.Op {
	return basic.Set("name", value)
}

func Description(value string) basic.Op {
	return basic.Set("description", value)
}

func Email(value string) basic.Op {
	return basic.SetChild("notification", "email", value)
}
