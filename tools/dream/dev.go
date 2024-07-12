//go:build dreamdev
// +build dreamdev

package main

import service "github.com/taubyte/tau/clients/http/dream"

func init() {
	service.Dev = true
}
