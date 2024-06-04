package services

import (
	"github.com/taubyte/tau/pkg/schema/basic"
)

func Id(value string) basic.Op {
	return basic.Set("id", value)
}

func Description(value string) basic.Op {
	return basic.Set("description", value)
}

func Tags(value []string) basic.Op {
	return basic.Set("tags", value)
}

func Protocol(value string) basic.Op {
	return basic.Set("protocol", value)
}

func SmartOps(value []string) basic.Op {
	return basic.Set("smartops", value)
}
