//go:build !unix

package jobs

import "context"

// shortcut: no cross-process lock off unix, monkeys only run on linux hosts.
func fileLock(context.Context, string) (func(), error) { return func() {}, nil }
