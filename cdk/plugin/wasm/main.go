//go:build wasi || wasm
// +build wasi wasm

package main

import (
	"fmt"
	"os"

	"github.com/extism/go-pdk"
	"github.com/taubyte/tau/pkg/schema/project"
)

//export t
func t() int32 {
	//input := pdk.Input()
	var out string
	// out += fmt.Sprintln(os.Mkdir("root", 0755))
	out += fmt.Sprintln(os.Stat("root"))
	_, err := project.Open(project.SystemFS("root"))
	if err != nil {
		out += err.Error()
	}
	pdk.OutputString(out)
	return 0
}

func main() {}
