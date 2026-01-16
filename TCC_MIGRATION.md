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
    compiler.WithLocal(gitDir),  // or compiler.WithInMemory(fs, path)
    compiler.WithBranch(meta.Repository.Branch),
}

// Add seer options if needed (for dev mode, DV keys, etc.)
// Note: Dev mode and DV keys may need to be handled via seer options
// This needs to be verified based on tcc implementation

compiler, err := compiler.New(opts...)
if err != nil {
    return err
}

// Compile
ctx := context.Background()
obj, err := compiler.Compile(ctx)
if err != nil {
    return err
}

// Access results
flat := obj.Flat()
object := flat["object"].(map[string]interface{})
indexes := flat["indexes"].(map[string]interface{})

// Publish to TNS (needs to be implemented)
// The old compiler.Publish() method needs to be reimplemented
// using the object and indexes from tcc
```

## Key Differences

1. **Initialization**: 
   - Old: `compile.CompilerConfig()` + `compile.New()`
   - New: `compiler.New()` with `WithLocal()` or `WithInMemory()`

2. **Options**:
   - Old: `compile.Dev()`, `compile.DVKey()`
   - New: `compiler.WithBranch()`, `compiler.WithLocal()`, `compiler.WithInMemory()`
   - **TODO**: Verify how dev mode and DV keys are handled in tcc

3. **Compilation**:
   - Old: `compiler.Build()` (no context needed)
   - New: `compiler.Compile(ctx)` (requires context)

4. **Result Access**:
   - Old: `compiler.Object()`, `compiler.Indexes()`
   - New: `obj.Flat()["object"]`, `obj.Flat()["indexes"]`

5. **Publishing**:
   - Old: `compiler.Publish(tns)` (built-in)
   - New: **Needs to be implemented** - extract object/indexes and use TNS client directly

6. **Logs**:
   - Old: `compiler.Logs()` returns `io.ReadSeeker`
   - New: **TODO**: Check if tcc provides logging mechanism

7. **Close**:
   - Old: `compiler.Close()` (implements `io.Closer`)
   - New: **TODO**: Check if tcc compiler needs closing

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
- Need to implement `Publish()` equivalent using TNS client
- Need to handle logging (currently copies logs to `c.LogFile`)
- Need to verify dev mode handling in tcc

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
   - See `pkg/config-compiler/compile/publish.go` for reference implementation

2. **Logs Method**:
   - Old compiler provides `Logs() io.ReadSeeker`
   - Need to verify if tcc provides logging or if it needs to be added

3. **Dev Mode and DV Keys**:
   - Old compiler has `compile.Dev()` and `compile.DVKey()` options
   - Need to verify how these are handled in tcc (may be via seer options)

4. **Generated Domain Regex**:
   - Old compiler accepts `generatedDomainRegExp` in `CompilerConfig()`
   - Need to verify if tcc handles this or if it needs to be added

5. **Metadata Handling**:
   - Old compiler requires `patrick.Meta` with repository info
   - TCC uses `WithBranch()` - need to verify if commit/provider/repo ID are needed

### Files That May Need Updates

1. **`pkg/config-compiler/compile/publish.go`**:
   - Contains the publish logic that needs to be reimplemented for tcc
   - Uses `specs.ProjectPrefix()` and `specsCommon.Current()`

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

- [ ] **Priority 1**: Migrate `services/monkey/jobs/config.go` (production code)
  - [ ] Replace `compile.CompilerConfig()` and `compile.New()`
  - [ ] Implement `Publish()` equivalent
  - [ ] Handle logging
  - [ ] Test with dev and production modes
  - [ ] Verify DV key handling

- [ ] **Priority 2**: Implement missing tcc features
  - [ ] Add `Publish()` method or helper function
  - [ ] Add logging support if needed
  - [ ] Verify dev mode and DV key options
  - [ ] Verify generated domain regex handling

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