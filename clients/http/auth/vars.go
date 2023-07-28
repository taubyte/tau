package client

import "time"

var (
	// DefaultTimeout is the default timeout for all requests
	DefaultTimeout = 3 * time.Second

	// CreateRepositoryRetryAttempts is the number of times to retry creating a repository
	CreateRepositoryRetryAttempts uint = 3

	// CreateRepositoryRetryDelay is the delay between retry attempts
	CreateRepositoryRetryDelay = 300 * time.Millisecond
)
