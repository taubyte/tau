# go-auth-http

[![Release](https://img.shields.io/github/release/taubyte/go-auth-http.svg)](https://github.com/taubyte/go-auth-http/releases)
[![License](https://img.shields.io/github/license/taubyte/go-auth-http)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/taubyte/go-auth-http)](https://goreportcard.com/report/taubyte/go-auth-http)
[![GoDoc](https://godoc.org/github.com/taubyte/go-auth-http?status.svg)](https://pkg.go.dev/github.com/taubyte/go-auth-http)
[![Discord](https://img.shields.io/discord/973677117722202152?color=%235865f2&label=discord)](https://tau.link/discord)

REST client for interfacing with the Taubyte Auth Node

## Installation

```
go get github.com/taubyte/go-auth-http
```


## Testing

The Mock server has been built in python. Make sure you install dependencies:
TODO: move mock to golang http mocks

```shell
pip3 install -r test_server/requirements.txt

go test -v ./...
```

