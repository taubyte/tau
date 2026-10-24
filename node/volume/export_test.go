package volume

import "github.com/taubyte/tau/pkg/containers/reserve"

// HoldTo makes v's restores leave floor free on the disk they write to.
func HoldTo(v *Volumes, floor reserve.Floor) { v.floor = floor }
