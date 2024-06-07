//go:build wasi || wasm
// +build wasi wasm

package main

import (
	"fmt"
	"os"

	"github.com/extism/go-pdk"
)

//export t
func t() int32 {
	//input := pdk.Input()
	out := "*-----*"
	//out += fmt.Sprintln(os.Mkdir("/mnt", 0755))
	out += fmt.Sprintln(os.Stat("/mnt/t"))
	out += fmt.Sprintln(os.Stat("/mnt"))

	// _, err := project.Open(project.SystemFS("/root"))
	// if err != nil {
	// 	out += err.Error()
	// }
	pdk.OutputString(out)
	return 0
}

func main() {}
