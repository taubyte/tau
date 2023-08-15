package http

import "time"

var DefaultTimeout = 10 * time.Second

const (
	Github    supportedProvider = "github"
	Bitbucket supportedProvider = "bitbucket"
)
