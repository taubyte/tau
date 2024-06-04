package images

import (
	"github.com/taubyte/tau/pkg/specs/builders/wasm"
)

const (
	// Taubyte Organization Name on DockerHub
	TaubyteOrganization = "taubyte"

	// Default Image Name Format
	ImageNameFormat = "%s/%s:%s"
)

// Repository Names For Taubyte Organization
const (
	RustRepository           = "rust-wasi"
	GoRepository             = "go-wasi"
	AssemblyScriptRepository = "assembly-script-wasi"

	TestExampleVersion = "test-examples"
)

// Container Image Tarballs
const (
	RustTarBallFile           = "rs.tar"
	GoTarBallFile             = "go.tar"
	AssemblyScriptTarBallFile = "as.tar"
)

const (
	TarBallBuildDir = "_builds"
	ProductionDir   = "production"
	TestExamplesDir = "test_examples"
)

// Image Environment Variables
var (
	RustImageEnvVar                  = imageEnvVar{(envVar("TAUBYTE_RUST_IMAGE"))}
	GoImageEnvVar        imageEnvVar = imageEnvVar{(envVar("TAUBYTE_GO_IMAGE"))}
	AssemblyScriptEnvVar imageEnvVar = imageEnvVar{(envVar("TAUBYTE_AS_IMAGE"))}
)

// Docker Credential Variables
const (
	UserEnvVar  envVar = "TAUBYTE_DOCKER_USER"
	TokenEnvVar envVar = "TAUBYTE_DOCKER_TOKEN"
)

var languageConfigs map[wasm.SupportedLanguage]LanguageConfig = map[wasm.SupportedLanguage]LanguageConfig{
	wasm.Rust: {
		language:    wasm.Rust,
		imageMethod: RustImage,
		tarBallName: RustTarBallFile,
	},
	wasm.Go: {
		language:    wasm.Go,
		imageMethod: GoImage,
		tarBallName: GoTarBallFile,
	},
	wasm.AssemblyScript: {
		language:    wasm.AssemblyScript,
		imageMethod: AssemblyScriptImage,
		tarBallName: AssemblyScriptTarBallFile,
	},
}
