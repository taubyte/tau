package builders

import (
	"io/fs"

	ci "github.com/taubyte/tau/pkg/containers"
)

type Environment struct {
	Image     string
	Variables map[string]string
}

type Config struct {
	Version     string
	Environment Environment
	Workflow    []string

	// TODO: Repo Fixer should remove all of these cases for websites and libraries
	Enviroment Environment
}

type Dir interface {
	Wasm() Wasm
	Website() Website
	CodeSource(string) string
	TaubyteDir() string
	ConfigFile() string
	DockerDir() DockerDirType
	DockerFile() string
	DefaultOptions(script string, outDir string, environment Environment) []ci.ContainerOption
	SetSourceVolume() ci.ContainerOption
	SetOutVolume(string) ci.ContainerOption
	SetBuildCommand(script string) ci.ContainerOption
	SetEnvironmentVariables() ci.ContainerOption
	String() string
}

type DockerDirType interface {
	String() string
	Stat() (fs.FileInfo, error)
	Tar() ([]byte, error)
}

type Wasm interface {
	WasmCompressed() string
	Zip() string
}

type Website interface {
	BuildZip() string
	SetWorkDir() ci.ContainerOption
}
