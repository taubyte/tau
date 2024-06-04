package wasm

const (
	Utils         = "utils"
	ConfigVersion = "0.1.0"
	BuildFile     = "build.sh"

	WasmFileName       = "main"
	WasmExt            = ".wasm"
	WasmCompressedExt  = ".zwasm"
	ZipFile            = "artifact.zip"
	WasmFile           = "main.wasm"
	DeprecatedWasmFile = "artifact.wasm"

	BufferSize = 1024
)

// Supported Languages
const (
	Rust           SupportedLanguage = "Rust"
	Go             SupportedLanguage = "Go"
	AssemblyScript SupportedLanguage = "Assembly_Script"
)

var supportedLanguages = map[SupportedLanguage]string{
	Rust:           ".rs",
	Go:             ".go",
	AssemblyScript: ".ts",
}

var languageAliases = map[SupportedLanguage][]string{
	Rust:           {"rs"},
	Go:             {"golang"},
	AssemblyScript: {"asm", "assembly"},
}

func SupportedLanguages() map[SupportedLanguage]string {
	return supportedLanguages
}

func LanguageAliases() map[SupportedLanguage][]string {
	return languageAliases
}
