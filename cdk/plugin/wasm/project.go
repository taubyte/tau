//go:build wasi || wasm
// +build wasi wasm

package main

import (
	"github.com/extism/go-pdk"
	proj "github.com/taubyte/tau/pkg/schema/project"
)

var project proj.Project

//export openProject
func openProject() int32 {
	var err error
	project, err = proj.Open(proj.SystemFS("/mnt"))
	if err != nil {
		pdk.SetError(err)
		return 1
	}

	return 0
}

//export projectGetName
func projectGetName() int32 {
	pdk.OutputString(project.Get().Name())
	return 0
}

//export projectSetName
func projectSetName() int32 {
	name := pdk.InputString()
	if err := project.Set(true, proj.Name(name)); err != nil {
		pdk.SetError(err)
		return 1
	}
	return 0
}

//export projectGetId
func projectGetId() int32 {
	pdk.OutputString(project.Get().Id())
	return 0
}

//export projectSetId
func projectSetId() int32 {
	id := pdk.InputString()
	if err := project.Set(true, proj.Id(id)); err != nil {
		pdk.SetError(err)
		return 1
	}
	return 0
}

//export projectGetDescription
func projectGetDescription() int32 {
	pdk.OutputString(project.Get().Description())
	return 0
}

//export projectSetDescription
func projectSetDescription() int32 {
	id := pdk.InputString()
	if err := project.Set(true, proj.Description(id)); err != nil {
		pdk.SetError(err)
		return 1
	}
	return 0
}

//export projectGetEmail
func projectGetEmail() int32 {
	pdk.OutputString(project.Get().Email())
	return 0
}

//export projectSetEmail
func projectSetEmail() int32 {
	email := pdk.InputString()
	if err := project.Set(true, proj.Email(email)); err != nil {
		pdk.SetError(err)
		return 1
	}
	return 0
}

//export projectGetTags
func projectGetTags() int32 {
	pdk.OutputJSON(project.Get().Tags())
	return 0
}

//export projectSetTags
func projectSetTags() int32 {
	pdk.SetErrorString("not implemented")
	return 1
}
