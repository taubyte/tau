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
    compiler.Branch("main"),
    compiler.SeerOptions(yaseerOptions...),
)
if err != nil {
    return err
}

result, err := compiler.Compile(ctx)
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
└── taubyte/         # Taubyte-specific compiler
    └── v1/
        ├── compiler.go    # Main compiler
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
- Test fixtures with example configurations
- Error location and validation tests


## License

See the main project LICENSE file.
