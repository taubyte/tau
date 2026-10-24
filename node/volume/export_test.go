package volume

import (
	"os"

	"github.com/taubyte/tau/pkg/containers/reserve"
)

// HoldTo makes v's restores leave floor free on the disk they write to.
func HoldTo(v *Volumes, floor reserve.Floor) { v.floor = floor }

// SyncWith makes v's restores make what they write durable through sync.
func SyncWith(v *Volumes, sync func(*os.File) error) { v.sync = sync }
