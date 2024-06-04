package functions

import (
	"github.com/taubyte/tau/pkg/schema/basic"
)

/*********************************** Common Values ***********************************/

func Id(value string) basic.Op {
	return basic.Set("id", value)
}

func Description(value string) basic.Op {
	return basic.Set("description", value)
}

func Tags(value []string) basic.Op {
	return basic.Set("tags", value)
}

func Type(value string) basic.Op {
	return basic.SetChild("trigger", "type", value)
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

func Source(value string) basic.Op {
	return basic.Set("source", value)
}

func SmartOps(value []string) basic.Op {
	return basic.Set("smartops", value)
}

/*********************************** HTTP Values ***********************************/

func Method(value string) basic.Op {
	return basic.SetChild("trigger", "method", value)
}

func Paths(value []string) basic.Op {
	return basic.SetChild("trigger", "paths", value)
}

func Domains(value []string) basic.Op {
	return basic.Set("domains", value)
}

/*********************************** P2P Values ***********************************/

func Command(value string) basic.Op {
	return basic.SetChild("trigger", "command", value)
}

func Protocol(value string) basic.Op {
	return basic.SetChild("trigger", "service", value)
}

/*********************************** Pub-Sub Values ***********************************/

func Channel(value string) basic.Op {
	return basic.SetChild("trigger", "channel", value)
}

/*********************************** Pub-Sub/P2P Values ***********************************/

func Local(value bool) basic.Op {
	return basic.SetChild("trigger", "local", value)
}
