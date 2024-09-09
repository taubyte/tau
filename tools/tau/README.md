<h2 align="center">
  <a href="https://taubyte.com" target="_blank" rel="noopener noreferrer">
    <picture>
      <source media="(prefers-color-scheme: dark)" srcset="images/tau-cli-logo-box-v2.png">
      <img width="80" src="images/tau-cli-logo-box-v2.png" alt="Tau CLI">
    </picture>
  </a>
  <br />
  Tau CLI
  
  ***Local Coding Equals Global Production***
</h2>
<div align="center">

[![Release](https://img.shields.io/github/release/taubyte/tau-cli.svg)](https://github.com/taubyte/tau/tools/tau/releases)
[![License](https://img.shields.io/github/license/taubyte/tau-cli)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/taubyte/tau-cli)](https://goreportcard.com/report/taubyte/tau-cli)
[![GoDoc](https://godoc.org/github.com/taubyte/tau/tools/tau?status.svg)](https://pkg.go.dev/github.com/taubyte/tau/tools/tau)
[![Discord](https://img.shields.io/discord/973677117722202152?color=%235865f2&label=discord)](https://discord.gg/wM8mdskh)

</div>

`tau` is a command-line interface (CLI) tool for interacting with [Taubyte-based Clouds](https://github.com/taubyte). It enables users to create, manage projects, applications, resources, and more directly from the terminal.


## Installation

### NPM
```bash
npm i @taubyte/cli
```

### Self extracting
```
curl https://get.tau.link/cli | sh
```

### Fetch and Install with Go
```shell
go install github.com/taubyte/tau/tools/tau@latest
```
You can rename `tau-cli` to `tau` or create an alias.

### Clone and Build
```shell
git clone https://github.com/taubyte/tau/tools/tau
cd tau
go build -o ~/go/bin/tau
```

### Offline version (Optional)
Fails faster if exploring an unregistered project
```shell
go build -o ~/go/bin/otau -tags=localAuthClient
```

## Login

`tau login`
    - opens selection with default already selected
    - simply logs in if only default available
    - will open new if no profiles found
`tau login --new` for new
  - `--set-default` for making this new auth the default
`tau login <profile-name>` for using a specific profile


## Environment Variables:
- `TAUBYTE_PROJECT` Selected project
- `TAUBYTE_PROFILE` Selected profile
- `TAUBYTE_APPLICATION` Selected application
- `TAUBYTE_CONFIG (default: ~/tau.yaml)` Config location
- `TAUBYTE_SESSION (default: /tmp/tau-<shell-pid>)` Session location
- `DREAM_BINARY (default: $GOPATH/dream)` Dream binary location

## Testing

### All tests
`go test -v ./...`

### Hot reload tests
`$ cd tests`

Edit [air config](tests/.air.toml#L8) `cmd = "go test -v --run <Function|Database|...> [-tags=no_rebuild]`

(Optional) Add `debug: true,` to an individual test

`$ air`

## Running Individual Prompts

`go run ./prompts/internal`

## Measuring Coverage:

### Calculate coverage for all packages
```shell
go test -v ./... -tags=localAuthClient,projectCreateable,localPatrick,cover,noPrompt -coverprofile cover.out -coverpkg ./...
```

### Display coverage for all packages
```
go tool cover -html=cover.out
go tool cover -func=cover.out
```

# Documentation
For documentation head to [tau.how](https://tau.how/docs/tau)
