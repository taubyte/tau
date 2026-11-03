package volume

import (
	"io"
	"os"

	"github.com/taubyte/tau/pkg/containers/reserve"
)

// HoldTo makes v's restores leave floor free on the disk they write to.
func HoldTo(v *Volumes, floor reserve.Floor) { v.floor = floor }

// SyncWith makes v's restores make what they write durable through sync.
func SyncWith(v *Volumes, sync func(*os.File) error) { v.sync = sync }

// ReadWith makes v's checkpoints read a file's data through read.
func ReadWith(v *Volumes, read func(*os.File, []byte, int64) (int, error)) { v.read = read }

// Walk is the archive a checkpoint of dir compresses, written to w.
func Walk(w io.Writer, dir string) (int64, error) { return tarInto(w, dir, (*os.File).ReadAt) }

// Encoding is how a checkpoint is compressed.
var Encoding = encoding
