//go:build ee

package main

import eenode "github.com/taubyte/tau/ee/node"

// An ee build composes every ee service onto the node it starts.
func init() { composed = append(composed, eenode.Options(eenode.Config{})...) }
