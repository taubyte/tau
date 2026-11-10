// Package b writes another package's var.
package b

import "github.com/taubyte/tau/a"

func Set() { a.Counter = 2 }
