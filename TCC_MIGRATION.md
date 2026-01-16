# TCC Migration Guide

This document identifies all places where the codebase needs to be migrated from the old `config-compiler` to the new `tcc` (Taubyte Configuration Compiler).

## Migration Pattern

### Old Pattern (config-compiler)
```go
import (
    "github.com/taubyte/tau/pkg/config-compiler/compile"
    projectSchema "github.com/taubyte/tau/pkg/schema/project"
)

// Create config
rc, err := compile.CompilerConfig(project, meta, generatedDomainRegExp)
if err != nil {
    return err
}

// Create compiler with options
compileOps := []compile.Option{}
if dev {
    compileOps = append(compileOps, compile.Dev())
} else {
    compileOps = append(compileOps, compile.DVKey(dvPublicKey))
}

compiler, err := compile.New(rc, compileOps...)
if err != nil {
    return err
}
defer compiler.Close()

// Build
err = compiler.Build()
if err != nil {
    return err
}

// Access results
object := compiler.Object()      // map[string]interface{}
indexes := compiler.Indexes()    // map[string]interface{}

// Publish to TNS
err = compiler.Publish(tns)
```

### New Pattern (tcc)
```go
import (
    "context"
    "github.com/taubyte/tau/pkg/tcc/taubyte/v1/compiler"
    projectSchema "github.com/taubyte/tau/pkg/schema/project"
)

// Create compiler with options
opts := []compiler.Option{
    compiler.WithLocal(gitDir),  // or compiler.WithVirtual(fs, path)
    compiler.WithBranch(meta.Repository.Branch),
}

// Note: Dev mode and DV keys are NOT handled via compiler options
// They need to be handled externally when processing validations
// See "Dev Mode and DV Keys" section below

compiler, err := compiler.New(opts...)
if err != nil {
    return err
}

// Compile (returns object, validations, and error)
ctx := context.Background()
obj, validations, err := compiler.Compile(ctx)
if err != nil {
    return err
}

// Process validations externally (for DNS validation with dev/DV keys)
for _, validation := range validations {
    if validation.Validator == "dns" {
        // Handle DNS validation with dev mode or DV key
        // See validation processing section
    }
}

// Access results
flat := obj.Flat()
object := flat["object"].(map[string]interface{})
indexes := flat["indexes"].(map[string]interface{})

// Publish to TNS (needs to be implemented)
// The old compiler.Publish() method needs to be reimplemented
// using the object and indexes from tcc
```

## Handling Validations

TCC returns validations that must be processed externally. This is a major architectural difference from the old compiler.

**Important**: Validation processing utilities should be implemented in `utils/tcc` package (not `pkg/tcc`) because they use libraries like `domain-validation` that won't compile to WASM/WASI.

### DNS Validation Example

**Note**: This validation logic should be implemented in `utils/tcc/validations.go` (not `pkg/tcc`) because it uses `domain-validation` library that won't compile to WASM/WASI.

```go
// In utils/tcc/validations.go
import (
    dv "github.com/taubyte/domain-validation"
    domainSpec "github.com/taubyte/tau/pkg/specs/domain"
    "github.com/taubyte/tau/pkg/tcc/engine"
)

// ProcessDNSValidations processes DNS validations from TCC compiler
func ProcessDNSValidations(
    validations []engine.NextValidation,
    generatedDomainRegExp *regexp.Regexp,
    devMode bool,
    dvPublicKey []byte,
    domainValPublicKeyData []byte, // default key for dev mode
) error {
    for _, validation := range validations {
        if validation.Validator == "dns" && validation.Key == "domain" {
            fqdn := validation.Value.(string)
            projectID := validation.Context["project"].(string)
            
            var err error
            if devMode {
                // Use default public key for dev mode
                err = domainSpec.ValidateDNS(
                    generatedDomainRegExp,
                    projectID,
                    fqdn,
                    true,  // dev mode
                    dv.PublicKey(domainValPublicKeyData), // default key
                )
            } else {
                // Use provided DV public key for production
                err = domainSpec.ValidateDNS(
                    generatedDomainRegExp,
                    projectID,
                    fqdn,
                    false,  // production mode
                    dv.PublicKey(dvPublicKey),
                )
            }
            
            if err != nil {
                return fmt.Errorf("DNS validation failed for %s: %w", fqdn, err)
            }
        }
    }
    return nil
}
```

### Validation Structure

```go
type NextValidation struct {
    Key       string                 // identifier (e.g., "domain")
    Value     interface{}            // the actual value to validate (e.g., FQDN string)
    Validator string                 // validator name (e.g., "dns", "cid")
    Context   map[string]interface{} // additional context (project, app, etc.)
}
```

## Key Differences

1. **Initialization**: 
   - Old: `compile.CompilerConfig()` + `compile.New()`
   - New: `compiler.New()` with `WithLocal()` or `WithVirtual(fs, path)`

2. **Options**:
   - Old: `compile.Dev()`, `compile.DVKey()`
   - New: `compiler.WithBranch()`, `compiler.WithLocal()`, `compiler.WithVirtual(fs, path)`
   - **IMPORTANT**: Dev mode and DV keys are NOT compiler options in tcc. They must be handled externally when processing DNS validations from the `validations` return value

3. **Compilation**:
   - Old: `compiler.Build()` (no context needed, returns error only)
   - New: `compiler.Compile(ctx)` (requires context, returns `Object, []NextValidation, error`)

4. **Result Access**:
   - Old: `compiler.Object()`, `compiler.Indexes()`
   - New: `obj.Flat()["object"]`, `obj.Flat()["indexes"]`

5. **Publishing**:
   - Old: `compiler.Publish(tns)` (built-in)
   - New: **Needs to be implemented in `utils/tcc` package** - extract object/indexes and use TNS client directly
   - **Note**: Must be in `utils/tcc` (not `pkg/tcc`) because TNS client won't compile to WASM/WASI

6. **Logs**:
   - Old: `compiler.Logs()` returns `io.ReadSeeker`
   - New: TCC returns errors that can be converted to `io.ReadSeeker` via helper in `utils/tcc` package
   - **Location**: Should be implemented in `utils/tcc/logs.go` (not `pkg/tcc`)
   - **Reason**: Cannot be in `pkg/tcc` because io operations won't compile to WASM/WASI

7. **Close**:
   - Old: `compiler.Close()` (implements `io.Closer`)
   - New: **Not needed** - TCC compiler does not require explicit closing

8. **Validations**:
   - Old: Validations were handled internally (DNS validation with dev/DV keys)
   - New: Returns `[]NextValidation` that must be processed externally. DNS validations include FQDN in `Value` and context in `Context` map.

## Production Code Locations

### 1. `services/monkey/jobs/config.go` ⚠️ **HIGH PRIORITY**
**Status**: Production code - actively used in monkey service

**Current Usage**:
- Uses `compile.CompilerConfig()` with `c.Job.Meta` and `c.GeneratedDomainRegExp`
- Uses `compile.New()` with `compile.Dev()` or `compile.DVKey()` options
- Uses `compiler.Build()`, `compiler.Publish()`, and `compiler.Logs()`
- Handles project validation before compilation

**Migration Notes**:
- Need to convert `c.gitDir` to `WithLocal()` option
- Need to extract branch from `c.Job.Meta.Repository.Branch`
- Need to use `Publish()` helper from `utils/tcc` package (see `pkg/config-compiler/compile/publish.go` for reference implementation)
  - **Note**: Must be in `utils/tcc` (not `pkg/tcc`) because it won't compile to WASM/WASI
- **Logging**: Use `Logs()` helper from `utils/tcc` package to convert TCC errors to `io.ReadSeeker`
  - **Note**: Must be in `utils/tcc` (not `pkg/tcc`) because io operations won't compile to WASM/WASI
- **Dev mode/DV keys**: Must use validation processing utilities from `utils/tcc` package
  - Process DNS validations from `validations` return value using `c.Monkey.Dev()` or `c.DVPublicKey`
  - **Note**: Validation utilities must be in `utils/tcc` (not `pkg/tcc`) because they won't compile to WASM/WASI

**Lines**: 12-55

---

## Test Code Locations

### 2. `dream/fixtures/compile.go`
**Status**: Test fixture helper

**Current Usage**:
- Uses `compile.CompilerConfig()` with fake metadata
- Uses `compile.New()` with `compile.Dev()`
- Uses `compiler.Build()` and `compiler.Publish()`

**Migration Notes**:
- Simple migration - just needs pattern update
- Used by dream test fixtures

**Lines**: 19-47

### 3. `dream/fixtures/indexer_test.go`
**Status**: Test file

**Current Usage**:
- Uses `compile.CompilerConfig()` and `compile.New()`
- Tests indexer functionality

**Lines**: 59, 65

### 4. `dream/fixtures/config_compiler_test.go`
**Status**: Test file

**Current Usage**:
- Multiple test cases using `compile.CompilerConfig()` and `compile.New()`
- Tests config compiler functionality

**Lines**: 82, 88, 198, 206, 220, 226

### 5. `dream/fixtures/decompile_prod_test.go`
**Status**: Test file

**Current Usage**:
- Uses `compile.CompilerConfig()` and `compile.New()`
- Tests decompile functionality

**Lines**: 75, 78

### 6. `dream/fixtures/publish_test.go`
**Status**: Test file

**Current Usage**:
- Uses `compile.CompilerConfig()` and `compile.New()`
- Tests publish functionality

**Lines**: 62, 68

### 7. `dream/fixtures/http_test.go`
**Status**: Test file

**Current Usage**:
- Uses `compile.CompilerConfig()` and `compile.New()`
- Tests HTTP functionality

**Lines**: 77, 83

### 8. `pkg/config-compiler/e2e_test.go`
**Status**: End-to-end test for config-compiler

**Current Usage**:
- Multiple test cases using `compile.CompilerConfig()` and `compile.New()`
- Comprehensive e2e tests

**Lines**: 46, 52, 118, 124, 160, 166, 193, 199

**Migration Notes**:
- This is testing the old compiler itself
- May need to keep as-is or create equivalent tcc e2e tests

### 9. `services/monkey/tests/job_test.go`
**Status**: Test file

**Current Usage**:
- Uses `compile.CompilerConfig()` and `compile.New()`
- Tests monkey job functionality

**Lines**: 114, 117

### 10. `services/hoarder/tests/storing_test.go`
**Status**: Test file

**Current Usage**:
- Uses `compile.CompilerConfig()` and `compile.New()`
- Tests hoarder storage functionality

**Lines**: 110, 116

### 11. `services/substrate/components/database/tests/all_test.go`
**Status**: Test file

**Current Usage**:
- Multiple test cases using `compile.CompilerConfig()` and `compile.New()`
- Tests database component

**Lines**: 98, 101, 282, 285

### 12. `services/substrate/components/storage/tests/all_test.go`
**Status**: Test file

**Current Usage**:
- Multiple test cases using `compile.CompilerConfig()` and `compile.New()`
- Tests storage component

**Lines**: 128, 131, 408, 411

---

## Additional Considerations

### Missing Features in TCC

1. **Publish Method**: 
   - The old compiler has a built-in `Publish(tns)` method
   - TCC doesn't appear to have this - needs to be implemented separately
   - **Location**: Should be implemented in `utils/tcc` package (not `pkg/tcc`)
   - **Reason**: Cannot be in `pkg/tcc` because it won't compile to WASM/WASI (uses TNS client, domain-validation, etc.)
   - See `pkg/config-compiler/compile/publish.go` for reference implementation

2. **Logs Method**:
   - Old compiler provides `Logs() io.ReadSeeker`
   - **TCC approach**: TCC returns errors that can be converted to `io.ReadSeeker` via helper function
   - **Location**: Should be implemented in `utils/tcc/logs.go` (not `pkg/tcc`)
   - **Reason**: Cannot be in `pkg/tcc` because io operations won't compile to WASM/WASI
   - Example usage:
     ```go
     // In utils/tcc/logs.go
     func Logs(err error) io.ReadSeeker {
         // Convert error to io.ReadSeeker
         // Similar to old compiler.Logs() behavior
     }
     ```

3. **Dev Mode and DV Keys**:
   - Old compiler has `compile.Dev()` and `compile.DVKey()` options
   - **TCC handles this differently**: Dev mode and DV keys are NOT compiler options
   - DNS validations are returned as `NextValidation` items with `Validator == "dns"`
   - The caller must process these validations externally, using dev mode or DV key as appropriate
   - **Location**: Validation processing utilities should be in `utils/tcc` package (not `pkg/tcc`)
   - **Reason**: Cannot be in `pkg/tcc` because domain-validation library won't compile to WASM/WASI
   - Example validation structure:
     ```go
     NextValidation{
         Key: "domain",
         Value: "example.com",  // FQDN string
         Validator: "dns",
         Context: map[string]interface{}{
             "project": "project-id",
             "app": "app-name",  // optional
         },
     }
     ```
   - See `pkg/config-compiler/indexer/dns.go` for reference on how DNS validation was done in old compiler

4. **Generated Domain Regex**:
   - Old compiler accepts `generatedDomainRegExp` in `CompilerConfig()`
   - **TCC handles this differently**: The generated domain regex should be used when processing DNS validations externally
   - The regex is not passed to the compiler, but should be used in the validation handler

5. **Metadata Handling**:
   - Old compiler requires `patrick.Meta` with repository info
   - TCC uses `WithBranch()` - need to verify if commit/provider/repo ID are needed

### Files That May Need Updates

1. **`pkg/config-compiler/compile/publish.go`**:
   - Contains the publish logic that needs to be reimplemented for tcc
   - Uses `specs.ProjectPrefix()` and `specsCommon.Current()`
   - **New location**: Should be reimplemented in `utils/tcc/publish.go` (not `pkg/tcc`)
   - **Reason**: Cannot be in `pkg/tcc` because it uses TNS client and won't compile to WASM/WASI

2. **`services/monkey/jobs/types.go`**:
   - References `compilerCommon.ConfigRepository` type (line 22, 62)
   - This is a type definition, not a compiler usage
   - May need to keep for backward compatibility or refactor

3. **`services/monkey/job.go`**:
   - May reference `compilerCommon.ConfigRepository`
   - Need to verify

### Files That Don't Need Migration

1. **`services/seer/gw_http.go`**:
   - Uses `decompile.New()` (line 54) - this is for decompiling, not compiling
   - Not related to compiler migration

### Reference Implementation

See `pkg/tcc/taubyte/v1/compile_test.go` for a working example of:
- How to use tcc compiler
- How to compare results with old compiler
- How to access `object` and `indexes` from tcc results

## Migration Checklist

- [x] **Priority 1**: Migrate `services/monkey/jobs/config.go` (production code)
  - [x] Replace `compile.CompilerConfig()` and `compile.New()`
  - [x] Use `Publish()` helper from `utils/tcc` package
  - [x] Use validation processing utilities from `utils/tcc` package
  - [x] Use `Logs()` helper from `utils/tcc` package to convert errors to `io.ReadSeeker`
  - [ ] Test with dev and production modes
  - [ ] Verify DV key handling

- [x] **Priority 2**: Implement missing tcc features in `utils/tcc` package
  - [x] Add `Publish()` helper function in `utils/tcc/publish.go` (reuse logic from `pkg/config-compiler/compile/publish.go`)
    - **Note**: Must be in `utils/tcc` (not `pkg/tcc`) because it won't compile to WASM/WASI
  - [x] Implement DNS validation handler in `utils/tcc/validations.go` that processes `NextValidation` items with dev mode/DV key support
    - **Note**: Must be in `utils/tcc` (not `pkg/tcc`) because domain-validation library won't compile to WASM/WASI
  - [x] Handle generated domain regex in DNS validation handler
  - [x] Implement `Logs()` helper function in `utils/tcc/logs.go` to convert TCC errors to `io.ReadSeeker`
    - **Note**: Must be in `utils/tcc` (not `pkg/tcc`) because io operations won't compile to WASM/WASI

- [ ] **Priority 3**: Migrate test files
  - [ ] Update `dream/fixtures/compile.go`
  - [ ] Update all test files listed above
  - [ ] Ensure tests still pass

- [ ] **Priority 4**: Cleanup
  - [ ] Remove old config-compiler dependencies where possible
  - [ ] Update documentation
  - [ ] Verify no other code depends on old compiler

## Notes

- The test file `pkg/tcc/taubyte/v1/compile_test.go` shows that tcc produces equivalent results to the old compiler
- There's a known bug in the old compiler regarding messaging inside apps (see line 54-56 of compile_test.go)
- TCC uses a different architecture with passes (pass1, pass2, pass3, pass4) instead of the old compiler's single-pass approach
- TCC compiler options: `WithLocal(path)`, `WithVirtual(fs, path)`, `WithBranch(branch)`
- TCC does NOT have `WithInMemory()` - use `WithVirtual(fs, path)` with an in-memory filesystem instead
- DNS validation is now external - the compiler returns `NextValidation` items that must be processed by the caller
- The old compiler's `Dev()` and `DVKey()` options are replaced by external validation processing
- **WASM/WASI Compatibility**: Helper functions that use TNS client, domain-validation library, io operations, or other non-WASM-compatible dependencies must be in `utils/tcc` (not `pkg/tcc`)
  - `Publish()` helper → `utils/tcc/publish.go`
  - DNS validation processing → `utils/tcc/validations.go`
  - `Logs()` helper (error to io.ReadSeeker) → `utils/tcc/logs.go`