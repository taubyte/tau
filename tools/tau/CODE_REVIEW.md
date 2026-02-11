# Code Review: tools/tau

Findings from a pass over the `tools/tau` codebase for bugs, robustness, and consistency.

---

## 1. Bugs / Correctness

### 1.1 `move_postfix.go`: Wrong inverse bool flag name for `--flag false`

**File:** `cli/args/move_postfix.go` (lines 51–53)

When the user writes `--generate-repository false`, the code rewrites it to a single inverse flag:

```go
arg = "-no-" + strings.ReplaceAll(arg, "-", "")
```

For `--generate-repository` this produces `-no-generaterepository`. The real inverse flag name (from `flags/bool.go`) is `--no-generate-repository`. So the parser may not recognize the rewritten form. The rewrite should preserve the original flag name (e.g. `--no-generate-repository`) so it matches `BoolWithInverseFlag`’s `inverseName()`.

---

### 1.2 `validate/helpers.go`: Regex error not checked

**File:** `validate/helpers.go` (line 16)

```go
match, _ = regexp.MatchString(exp[1], val)  // error return not used
```

If `exp[1]` is an invalid regex, `MatchString` returns an error. Not checking it means invalid patterns are treated as “no match” and the code returns `errors.New(exp[0])`, which can hide the real problem (invalid regex). Prefer validating or compiling the regex and handling the error (e.g. return or wrap it).

---

### 1.3 `validate/helpers.go`: Possible panic on short `exp`

**File:** `validate/helpers.go` (lines 15–19)

`exp` is used as `exp[0]` and `exp[1]`. If any element of `expressions` has `len(exp) < 2`, the code panics. There is no guard. Callers must ensure each slice has at least two elements, or the function should check and return a clear error.

---

### 1.4 `common/helpers.go`: `PanicIfMissingValue` panics if `h` is nil

**File:** `cli/commands/resources/common/helpers.go` (line 6)

`reflect.TypeOf(h).Elem()` is used without checking `h`. If `h` is nil, this panics. Document that `h` must be a non-nil pointer to a struct, or add a nil check and return/panic with a clear message.

---

## 2. Error handling / Unused return values

### 2.1 `config.GetSelectedProject()` / `GetSelectedApplication()` return values not checked

**Files:** Multiple (e.g. `prompts/application/select.go`, `prompts/project/select.go`, `cli/commands/current/command.go`, `cli/commands/dream/build/helpers.go`, `prompts/domains.go`, `prompts/service.go`, `prompts/source.go`)

The second return value (error or ok) is not checked: e.g. `config.GetSelectedProject()` or `config.GetSelectedApplication()`.

- For `GetSelectedProject()` (returns `(string, error)`), not checking the error can hide config/selection problems and leave `name` empty, leading to confusing behavior.
- For `GetSelectedApplication()` (returns `(string, bool)`), the second value is “exists”; ignoring it is often acceptable if the code only needs the name.

Recommendation: Where `GetSelectedProject()` is used, handle the error (e.g. log, return, or prompt) so failures are visible.

---

### 2.2 `project/delete.go`: Return values from `schema.Get().Libraries(app)` / `Websites(app)` not used

**File:** `cli/commands/resources/project/delete.go` (lines 187, 197)

```go
libNames, _ = schema.Get().Libraries(app)
webNames, _ = schema.Get().Websites(app)
```

The error returns are not used. If the schema call fails, the code continues with empty or stale names and may skip unregistering libraries/websites or show misleading results. Consider propagating or handling the error.

---

## 3. Tests

### 3.1 Duplicate test case names in `args_test.go`

**File:** `cli/args/args_test.go`

- Two cases use the name `"sub"` (around lines 64 and 94).
- Two cases use the name `"true attached on a bool flag parse"` (around lines 130 and 137).

Duplicate names make it harder to see which case failed when running tests with `-v`. Use distinct names (e.g. `"sub login"`, `"sub function"`, `"bool true attached"`, `"bool true at end"`) or include a short description of the scenario.

---

### 3.2 `args_test.go`: Error handling in test

**File:** `cli/args/args_test.go` (lines 21–25)

```go
realApp, err := tauCLI.New()
if err != nil {
    t.Error(err)
    return
}
```

Using `t.Error` and `return` is fine, but if `New()` fails, later cases that use `realApp` are skipped without a clear “skip” signal. Consider `t.Fatal(err)` or `t.Skipf("tauCLI.New: %v", err)` so the test output is clearer.

---

## 4. Robustness / Edge cases

### 4.1 `prompts/string.go`: `c.Set(field, "")` can panic

**File:** `prompts/string.go` (lines 77–81, 126–130)

`c.Set(field, "")` is called and on error the code panics. That’s strict but can crash the process on unexpected CLI behavior. Depending on desired behavior, consider returning an error or logging and continuing instead of panicking.

---

### 4.2 `session/session.go`: Panic in `getOrCreateSession()`

**File:** `session/session.go` (lines 13–16)

`loadSession()` failure causes `panic(err)`. That’s consistent with “session is required” but makes the process exit hard. Document that session load failures are fatal, or consider returning an error so callers can decide.

---

### 4.3 Global mutable state: `prompts.UseDefaults`

**File:** `prompts/vars.go`, set in `cli/new.go` Before hook

`prompts.UseDefaults` is a global variable set from the CLI context. If multiple goroutines ever run commands concurrently, this could be a data race. Currently usage appears single-threaded; if concurrency is added, consider passing the value through context or arguments instead of a global.

---

## 5. Minor / Style / TODOs

### 5.1 Inconsistent inverse-flag handling

`move_postfix.go` builds a synthetic inverse flag for `--flag false`. The result does not match the actual inverse flag names produced by `BoolWithInverseFlag` (e.g. `--no-generate-repository`). Aligning this with the real flag names (see 1.1) would avoid subtle parsing issues.

---

### 5.2 TODOs that affect behavior

- **prompts/domains.go (line 171):** “confirm len(flagDomains) == len(domains) and warn that given flags were invalid” — invalid flags may currently be silently dropped or misinterpreted.
- **validate/variable.go (lines 62, 78, 115):** “TODO validate”, “TODO REGEX”, “add || gitlab || bitbucket” — validation and provider support may be incomplete.
- **prompts/entry_point.go (line 15):** “TODO better validator” — entry point validation may be weak.
- **session/discovery.go (line 14):** `debugEnabled()` reads `os.Getenv("DEBUG")` on every call; consider caching if this is hot (e.g. in discovery loops).

---

## 6. Summary

| Severity | Count | Area |
|----------|--------|------|
| Bug / correctness | 4 | Inverse flag name, regex error, exp bounds, nil panic |
| Error handling | 2 | Return values not checked (config, schema) |
| Tests | 2 | Duplicate names, skip vs fatal on New() failure |
| Robustness | 3 | Panics on Set, session panic, global UseDefaults |
| Minor / TODOs | 4 | Inconsistent flags, TODOs, debug env read |

**Suggested priorities:** Fix the inverse-flag name in `move_postfix.go` (1.1), add bounds check or contract for `exp` in `validate/helpers.go` (1.3), and handle or log errors from `schema.Get().Libraries/Websites` in `project/delete.go` (2.2) and from `GetSelectedProject()` (2.1). Then address regex error handling in validate (1.2).

---

## Second pass (additional findings)

### 7. Bugs / Correctness (second pass)

#### 7.1 `prompts/login/token_web.go`: Wrong length check before `sessionSplit[1]`

**File:** `prompts/login/token_web.go` (lines 27–33)

```go
sessionSplit := strings.Split(session, ".")
if len(sessionSplit) < 1 {
    err = fmt.Errorf("invalid session: `%s`", session)
    return
}
base64Decoded, err := base64.RawStdEncoding.DecodeString(sessionSplit[1])
```

`strings.Split` always returns at least one element, so `len(sessionSplit) < 1` is never true. The code then uses `sessionSplit[1]` (payload for JWT), so at least two parts are required. The check should be `len(sessionSplit) < 2` to avoid a panic when the session string has no dot.

---

#### 7.2 `project/helpers.go`: Possible nil dereference in `removeFromGithub`

**File:** `cli/commands/resources/project/helpers.go` (lines 125–127)

```go
if res, err := client.Repositories.Delete(ctx, user, name); err != nil {
    var deleteRes deleteRes
    data, err := io.ReadAll(res.Body)
```

When `Delete` returns an error (e.g. network failure), `res` may be nil. Using `res.Body` without checking `res != nil` can panic. Guard with `if res != nil { ... }` before reading the body.

---

#### 7.3 `prompts/tables.go`: Panics on malformed or empty rows

**File:** `prompts/tables.go` (RenderTable and RenderTableWithMerge)

- **Index out of range:** Each `item` is used as `item[0]` and `item[1]`. If any row has `len(item) < 2`, the code panics. No validation of `data` is done.
- **Division by zero:** `width_to_length := width / (len(item[1]) + len(item[0]))`. When both strings are empty, the divisor is 0 and the code panics.
- **Negative slice:** `desired_length` can be negative (e.g. `termSize/width_to_length - len(item[0]) - whitespace - trailingperiod`), then `item[1][:desired_length]` panics.

Add checks: require `len(item) >= 2`, skip or guard when `len(item[0])+len(item[1]) == 0`, and ensure `desired_length >= 0` (or clamp) before slicing.

---

### 8. Resource leaks (second pass)

#### 8.1 `config/load.go`: Config file handle never closed

**File:** `config/load.go` (lines 14–19)

```go
if !file.Exists(constants.TauConfigFileName) {
    _, err := os.Create(constants.TauConfigFileName)
    if err != nil {
        return singletonsI18n.CreatingConfigFileFailed(err)
    }
}
```

The file returned by `os.Create` is never closed, leaking a file descriptor. Call `Close()` (and ignore or handle the error) after a successful create.

---

#### 8.2 `logs/query.go`: `LogFile` ReadCloser never closed

**File:** `cli/commands/resources/logs/query.go` (lines 60–69)

```go
log, err := patrickC.LogFile(jobId, cid)
if err != nil {
    return err
}
data, err := io.ReadAll(log)
```

`log` is an `io.ReadCloser` and is never closed, so resources (e.g. HTTP response body) can leak. Use `defer log.Close()` after a successful `LogFile` call, or close after `ReadAll` before the next iteration.

---

### 9. Second-pass summary

| Severity        | Count | Area                                                                 |
|----------------|-------|----------------------------------------------------------------------|
| Bug/correctness| 3     | token_web length check, removeFromGithub nil res, tables panic/div0 |
| Resource leak  | 2     | config Create, logs LogFile ReadCloser                              |

**Second-pass priorities:** Fix `sessionSplit` length check in token_web (7.1), guard `res` in removeFromGithub (7.2), close the config file in load.go (8.1), and close the log ReadCloser in logs/query.go (8.2). Then harden prompts/tables.go (7.3).

---

## Deep review: `session/discovery.go`

### 10. Core design problem: LCP on root-side PIDs cannot distinguish sibling sessions

The discovery algorithm stores the **root-most** `sessionAncestorDepth` (6) PIDs in the filename and matches sessions by longest-common-prefix (LCP) from the root side with a threshold of 2. The root-most PIDs are the *least* distinguishing: they are shared by every process on the machine (init, systemd, sshd master, user session manager, etc.). The PIDs that actually distinguish one terminal session from another are the **leaf-most** ones (the shell, the terminal emulator, tmux server), which are truncated away when the tree is deeper than 6.

**Concrete scenario — two gnome-terminal tabs (or two VS Code terminals):**

```
Tab A:  sshd(1000) → sshd(1500) → systemd-user(2000) → gnome-shell(2500) → gnome-terminal(3000) → bash(5000)
Tab B:  sshd(1000) → sshd(1500) → systemd-user(2000) → gnome-shell(2500) → gnome-terminal(3000) → bash(6000)
```

Root-first P_A = `[1000, 1500, 2000, 2500, 3000, 5000]` (6 elements — fits exactly).
Root-first P_B = `[1000, 1500, 2000, 2500, 3000, 6000]`.

Session file created by A stores: `[1000, 1500, 2000, 2500, 3000, 5000]`.
When B discovers: LCP(P_B, stored) = **5** (diverges only at the last element). Threshold = 2. So B reuses A's session. Both tabs share the same selected-project, selected-app, etc. — **cross-session pollution**.

**Concrete scenario — two separate tmux servers:**

```
tmux A pane:  sshd(1000) → sshd(1500) → bash(2000) → tmux-server-A(3000) → bash(5000)
tmux B pane:  sshd(1000) → sshd(1500) → bash(4000) → tmux-server-B(4500) → bash(7000)
```

P_A = `[1000, 1500, 2000, 3000, 5000]`, P_B = `[1000, 1500, 4000, 4500, 7000]`.
LCP = **2** (diverges at index 2). Threshold = 2. So 2 >= 2 → **B reuses A's session**, even though they are completely independent tmux instances from different login shells.

---

### 11. `sessionAncestorDepth = 6` is too shallow

On deeper process trees (common on desktop Linux, IDE terminals, containers), only the 6 root-most PIDs are stored. When the real tree is 10+ deep, the stored PIDs are all shared infrastructure (init, systemd, sshd, user-session, window-manager, terminal-emulator) and carry no session-distinguishing information.

Increasing to 16 would capture the leaf-side PIDs that actually differ between sessions. But this alone doesn't fix the LCP-from-root problem (see 10 above).

---

### 12. `sessionDirBaseNameFromRootPath` truncates from the wrong end

**File:** `session/discovery.go` (line 160)

```go
func sessionDirBaseNameFromRootPath(rootFirstPath []int, _ int64) string {
    six := make([]int, sessionAncestorDepth)
    for i := 0; i < sessionAncestorDepth; i++ {
        if i < len(rootFirstPath) {
            six[i] = rootFirstPath[i]
        }
    }
```

When `len(rootFirstPath) > sessionAncestorDepth`, the function keeps `rootFirstPath[0..5]` (root-most) and drops everything else. The discarded tail contains the shell PID, terminal PID, and other leaf-side processes that are the actual session discriminators. Should keep the **leaf-most** PIDs (the tail of rootFirstPath), or store the full path (up to the new depth).

---

### 13. `sessionDirBaseNameFromRootPath` silently ignores the timestamp parameter

**File:** `session/discovery.go` (line 160)

```go
func sessionDirBaseNameFromRootPath(rootFirstPath []int, _ int64) string {
```

The timestamp is accepted but discarded (named `_`). The comment says "No timestamp so the same process tree always maps to the same file." But the consequence is: **two processes with the same root-6 PIDs always collide into the same file**. On deep trees where root-6 are shared system PIDs, unrelated sessions overwrite each other's state.

Meanwhile `sessionDirBaseName` (line 178) *does* include a timestamp, creating a different naming scheme. Having two incompatible naming conventions makes the code harder to reason about and maintain.

---

### 14. `Delete()` uses no threshold — can delete the wrong session

**File:** `session/delete.go` (lines 36–56)

```go
for _, path := range matches {
    // ...
    L := longestCommonPrefixLength(P, S)
    if L > bestL || (L == bestL && ts > bestTs) {
        bestL = L
        bestTs = ts
        bestFile = path
    }
}
if bestFile == "" {
    return singletonsI18n.SessionNotFound()
}
```

Unlike `discoverOrCreateConfigFileLoc` which requires `bestL >= sessionCommonRootThreshold`, `Delete()` picks the file with the *highest* LCP regardless of how low it is (even LCP=1). If the actual session file doesn't exist but a weakly-matching file from another session does, `Delete()` removes it. Should apply the same threshold check before deleting.

---

### 15. `ensureExactSessionDir` uses `sessionAncestorDepth` (6) while discovery uses `maxAncestorDepthForPath` (20)

**File:** `session/discovery.go` (line 248 vs 257)

- `currentSessionPidList()` collects `ancestorPIDs(sessionAncestorDepth)` — 6 ancestors.
- `discoverOrCreateConfigFileLoc()` collects `ancestorPathFromRoot(maxAncestorDepthForPath)` — 20 ancestors.

When `setKey` calls `ensureExactSessionDir`, it builds the "exact" session dir from 6 PIDs. But discovery may have found a session based on a 20-ancestor comparison. The two views of "which session am I in" can disagree, especially on deep trees where the 6-PID exact path doesn't match what discovery selected.

---

### 16. No stale-session cleanup — dead PIDs persist forever

Session files in `/tmp/tau/` are never cleaned up. When a shell exits, its PID gets recycled. A stale session file with PIDs from a long-dead process tree can match a new, unrelated process tree that happens to reuse some of those PIDs, causing state leakage. There is no mechanism to:
- Check if the PIDs in a session filename are still alive.
- Expire session files after a timeout.
- Clean up on shell exit.

On long-running machines, `/tmp/tau/` can accumulate many stale files, increasing the chance of false LCP matches.

---

### 17. LCP tie-breaking by timestamp can pick the wrong session

**File:** `session/discovery.go` (line 291)

```go
if L > bestL || (L == bestL && ts > bestTs) {
```

When two session files have the same LCP, the one with the newer timestamp wins. But a newer timestamp just means the file was created more recently — it says nothing about whether that session is the *right* one for the current process tree. A stale session with a higher timestamp from an unrelated (but PID-overlapping) tree would win over the correct session with a lower timestamp.

---

### 18. Summary of discovery issues

| # | Issue | Impact |
|---|-------|--------|
| 10 | LCP on root-side PIDs can't distinguish sibling sessions | Cross-session pollution (shared state between terminals) |
| 11 | `sessionAncestorDepth=6` too shallow | On deep trees, stored PIDs are all shared infrastructure |
| 12 | Truncation from wrong end | Leaf-side (session-distinguishing) PIDs are discarded |
| 13 | Timestamp silently ignored in `sessionDirBaseNameFromRootPath` | Unrelated sessions with same root-6 collide into one file |
| 14 | `Delete()` has no threshold | Can delete another session's file |
| 15 | `ensureExactSessionDir` vs discovery use different depths | Write path and read path can disagree on "current session" |
| 16 | No stale-session cleanup | Dead PIDs cause false matches after PID recycling |
| 17 | Timestamp tie-breaking picks recency, not correctness | Wrong session can win on equal LCP |

**Recommended direction:** Increase ancestor depth to 16. Store the **leaf-most** (closest-to-tau) PIDs rather than root-most, since they are the actual session discriminators. Consider matching on longest-common-*suffix* (from the leaf side) instead of prefix (from the root side), or require that the stored leaf PID is still alive and is an ancestor of the current process. Add a stale-file cleanup mechanism (e.g. check if root PID in filename is still alive). Apply the threshold in `Delete()` as well.
