package usage_test

import (
	"fmt"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDisk(t *testing.T) {
	var stat unix.Statfs_t

	wd, err := os.Getwd()
	if err != nil {
		t.Error(err)
		return
	}

	err = unix.Statfs(wd, &stat)
	if err != nil {
		t.Error(err)
		return
	}

	// Available blocks * size per block = available space in bytes

	available := stat.Bavail * uint64(stat.Bsize)
	free := stat.Bfree * uint64(stat.Bsize)
	used := (stat.Blocks - stat.Bfree) * uint64(stat.Bsize)
	total := stat.Blocks * uint64(stat.Bsize)

	fmt.Println("Total    : ", total)
	fmt.Println("Free     : ", free)
	fmt.Println("Used     : ", used)
	fmt.Println("Available: ", available)

	// convert to GB and display
	fmt.Println("Total    : ", total/1024/1024/1024)
	fmt.Println("Free     : ", free/1024/1024/1024)
	fmt.Println("Used     : ", used/1024/1024/1024)
	fmt.Println("Available: ", available/1024/1024/1024)
}
