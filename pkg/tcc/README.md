# TCC - Taubyte Configuration Compiler

TCC (Taubyte Configuration Compiler) is a schema-driven configuration parsing and transformation engine. It provides a flexible framework for parsing, validating, and transforming structured configuration files (primarily YAML) into typed object hierarchies.

## Overview

TCC consists of four main components:

1. **Engine** - Core parsing and validation engine based on schema definitions
2. **Object** - Object-oriented interface for working with configuration data
3. **Transform** - Transformation pipeline for processing configuration objects
4. **Taubyte/v1** - Taubyte-specific compiler implementation with multi-pass processing

## Architecture

### Engine (`engine/`)

The engine is the core component that parses configuration files according to a schema definition. It uses the `yaseer` package to query and load configuration data.

**Key Features:**
- Schema-based validation
- Type-safe attribute definitions
- Path-based attribute mapping
- Custom validators
- Compatibility path support

**Example:**
```go
schema := SchemaDefinition(Root(
    String("name", Required()),
    Int("port", Default(8080)),
))

engine, err := engine.New(schema, yaseerOptions...)
if err != nil {
    return err
}

obj, err := engine.Process()
```

### Object (`object/`)

Provides a generic, type-safe interface for working with configuration objects. Objects can be queried, modified, and traversed using a selector pattern.

**Key Features:**
- Type-safe getters (`GetString`, `GetInt`, `GetBool`)
- Path-based navigation (`Fetch`, `CreatePath`)
- Pattern matching (`Match` with various match types)
- Child management (`Children`, `Child`)
- Attribute manipulation (`Set`, `Get`, `Delete`, `Move`)

**Data Types:**
- `Opaque` - Raw byte data
- `Refrence` - Reference to other objects

### Transform (`transform/`)

A transformation pipeline system that processes objects through a series of transformers. Each transformer receives a context and object, and returns a modified object.

**Key Features:**
- Context-based transformation with shared store
- Forkable contexts for hierarchical processing
- Type-safe store for strings, bytes, and objects
- Pipeline composition

**Example:**
```go
ctx := transform.NewContext[object.Refrence](context.Background())
result, err := transform.Pipe(ctx, obj, transformer1, transformer2, transformer3)
```

### Taubyte Compiler (`taubyte/v1/`)

A complete compiler implementation for Taubyte project configurations. It processes configurations through multiple passes:

1. **Pass 1** - Initial parsing and resource collection (applications, databases, domains, functions, libraries, messaging, services, smartops, storages, websites)
2. **Pass 2** - Function and smartops processing
3. **Pass 3** - Chroot operations
4. **Pass 4** - Final validation and resource-specific processing

**Usage:**
```go
compiler, err := compiler.New(
    compiler.WithBranch("main"),
    compiler.WithLocal("path/to/config"),
)
if err != nil {
    return err
}

result, validations, err := compiler.Compile(ctx)
if err != nil {
    return err
}

// Process validations externally
for _, v := range validations {
    // Implement validation logic based on v.Validator
}
```

### Taubyte Decompiler (`taubyte/v1/decompile/`)

The decompiler reverses the compilation process, converting compiled configuration objects back to YAML files. It uses a reverse transformation pipeline to restore the original structure and writes the results to the filesystem using the engine's schema.

**Key Features:**
- Reverse transformation pipeline (pass1 → pass2 → pass3)
- Automatic YAML file generation using schema definitions
- Value validation during decompilation
- Support for both local and virtual filesystems

**Reverse Passes:**
1. **Pass 1** - Unwraps root object (chroot reverse)
2. **Pass 2** - Resolves resource IDs back to names (functions, smartops, websites)
3. **Pass 3** - Restores attribute names and formats (all resources)

**Usage:**
```go
// Compile first
compiler, err := compiler.New(
    compiler.WithLocal("path/to/config"),
    compiler.WithBranch("main"),
)
if err != nil {
    return err
}

obj, validations, err := compiler.Compile(ctx)
if err != nil {
    return err
}

// Decompile to in-memory filesystem
memFs := afero.NewMemMapFs()
decompiler, err := decompile.New(decompile.WithVirtual(memFs, "/"))
if err != nil {
    return err
}

err = decompiler.Decompile(obj)
if err != nil {
    return err
}

// Or decompile to local filesystem
decompiler, err := decompile.New(decompile.WithLocal("output/path"))
if err != nil {
    return err
}

err = decompiler.Decompile(obj)
```

**Options:**
- `WithLocal(path)` - Decompile to local filesystem at the given path
- `WithVirtual(fs, path)` - Decompile to a virtual filesystem (e.g., `afero.MemMapFs`)

**Note:** The decompiler modifies the input object in place (same as regular compilation transforms). If you need to preserve the original object, make a copy before decompiling.

### External Validators

TCC supports external validators to keep complex validation logic (like DNS validation with domain-validation library) outside the compiler, maintaining WASM/WASI compatibility.

**NextValidation Structure:**
```go
type NextValidation struct {
    Key       string                 // identifier (e.g., "domain", "fqdn")
    Value     interface{}            // the actual value to validate
    Validator string                 // validator name (e.g., "dns", "cid")
    Context   map[string]interface{} // additional context for validation
}
```

**Example NextValidation:**
```json
{
  "key": "domain",
  "value": "example.com",
  "validator": "dns",
  "context": {
    "project": "proj-456",
    "app": "app-name"
  }
}
```

**Processing Validations:**
The compiler emits `NextValidation` items during compilation. It's up to the caller to implement and process these validations externally:

```go
obj, validations, err := compiler.Compile(ctx)
if err != nil {
    return err
}

for _, validation := range validations {
    switch validation.Validator {
    case "dns":
        // Implement DNS validation using validation.Value (FQDN)
        // Use validation.Context for project/app context
        err := validateDNS(validation.Value.(string), validation.Context)
        if err != nil {
            return fmt.Errorf("DNS validation failed: %w", err)
        }
    case "cid":
        // Implement CID validation
        // ...
    }
}
```

## Schema Definition

Schemas are defined using a node-based structure:

```go
schema := SchemaDefinition(
    Root(
        // Attributes at root level
        String("name", Required()),
        Int("port", Default(8080)),
        
        // Child groups
        DefineGroup("services",
            DefineIter(
                String("id", Required()),
                String("type"),
            ),
        ),
    ),
)
```

### Attribute Options

- `Required()` - Attribute must be present
- `Default(value)` - Default value if not present
- `Path(...)` - YAML path mapping
- `Compat(...)` - Compatibility path for deprecated fields
- `Key()` - Value is used as map key
- `InSet(...)` - Value must be in allowed set
- `IsCID()`, `IsEmail()`, `IsFqdn()`, etc. - Built-in validators

## Directory Structure

```
pkg/tcc/
├── engine/          # Core parsing engine
│   ├── engine.go    # Engine interface and implementation
│   ├── schema.go    # Schema definition
│   ├── node.go      # Node structure and loading
│   ├── types.go     # Type definitions
│   └── ...
├── object/          # Object interface
│   ├── generic.go   # Generic object implementation
│   ├── resolver.go  # Path resolution
│   └── types.go     # Type definitions
├── transform/       # Transformation pipeline
│   ├── pipe.go      # Pipeline execution
│   ├── context.go   # Transformation context
│   └── types.go     # Type definitions
├── validators.go    # NextValidation structure for external validators
└── taubyte/         # Taubyte-specific compiler
    └── v1/
        ├── compiler.go    # Main compiler
        ├── decompile/     # Decompiler implementation
        │   ├── decompiler.go    # Decompiler interface
        │   ├── pass1/           # First reverse pass (chroot)
        │   ├── pass2/           # Second reverse pass (ID resolution)
        │   └── pass3/           # Third reverse pass (attribute restoration)
        ├── schema/        # Taubyte schema definitions
        ├── pass1/         # First pass transformers
        ├── pass2/         # Second pass transformers
        ├── pass3/         # Third pass transformers
        ├── pass4/         # Fourth pass transformers
        └── utils/         # Utility functions
```

## Testing

The package includes comprehensive test coverage:
- Unit tests for each component
- Integration tests for the full compiler
- Decompiler round-trip tests (compile → decompile → recompile)
- Test fixtures with example configurations
- Error location and validation tests

**Coverage:**
- Engine: >87% coverage
- Decompile passes: >80% coverage each (pass1: 87.5%, pass2: 93.5%, pass3: 89.3%)


## License

See the main project LICENSE file.
