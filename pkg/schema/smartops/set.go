package smartops

import "github.com/taubyte/tau/pkg/schema/basic"

func Id(value string) basic.Op {
	return basic.Set("id", value)
}

func Description(value string) basic.Op {
	return basic.Set("description", value)
}

func Tags(value []string) basic.Op {
	return basic.Set("tags", value)
}

func Source(value string) basic.Op {
	return basic.Set("source", value)
}

func Timeout(value string) basic.Op {
	return basic.SetChild("execution", "timeout", value)
}

func Memory(value string) basic.Op {
	return basic.SetChild("execution", "memory", value)
}

func Call(value string) basic.Op {
	return basic.SetChild("execution", "call", value)
}
