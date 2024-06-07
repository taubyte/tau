//go:build wasi || wasm
// +build wasi wasm

package main

import (
	"fmt"
	"os"

	"github.com/extism/go-pdk"
	functions "github.com/taubyte/tau/pkg/schema/functions"
	"github.com/taubyte/tau/pkg/schema/project"

	_ "github.com/extism/go-pdk/wasi-reactor"
)

// //export __wasm_call_ctors
// func __wasm_call_ctors()

// //export _initialize
// func _initialize() {
// 	__wasm_call_ctors()
// }

//export t
func t() int32 {
	//input := pdk.Input()
	out := "*-----*"
	defer func() {
		pdk.OutputString(out)
	}()
	//out += fmt.Sprintln(os.Mkdir("/mnt", 0755))
	out += fmt.Sprintln(os.Stat("/mnt/t"))
	out += fmt.Sprintln(os.Stat("/mnt"))

	p, err := project.Open(project.SystemFS("/mnt"))
	if err != nil {
		out += err.Error()
		return 1
	}
	out += fmt.Sprintln("Got project", p)

	out += fmt.Sprintln("Set ID", p.Set(true, project.Id("fakeid")))

	f, err := p.Function("func1", "")
	if err != nil {
		out += err.Error()
		return 1
	}

	out += fmt.Sprintln("Set Function", f.Set(true,
		functions.Id("fake_func_id"),
		functions.Method("GET"),
	))

	return 0
}

func main() {}
