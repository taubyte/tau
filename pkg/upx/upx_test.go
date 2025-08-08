package upx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUPX_Compression(t *testing.T) {
	ctx := context.Background()
	upx, err := New(ctx)
	require.NoError(t, err, "Failed to initialize UPX")

	defer func() {
		require.NoError(t, upx.Close(ctx), "Failed to close UPX runtime")
	}()

	tempDir := t.TempDir()
	testBinary := createTestBinary(t, tempDir)

	compressedBinary := filepath.Join(tempDir, "testbin.compressed")

	originalSize := fileSize(t, testBinary)
	t.Logf("Original size: %d bytes", originalSize)

	err = upx.CompressFile(ctx, testBinary, compressedBinary)
	require.NoError(t, err, "Failed to compress test binary")

	compressedSize := fileSize(t, compressedBinary)
	t.Logf("Compressed size: %d bytes", compressedSize)

	require.Greater(t, compressedSize, int64(0), "Compressed file should not be empty")
	require.Less(t, compressedSize, originalSize, "Compressed file should be smaller than original")

	originalSizeAfter := fileSize(t, testBinary)
	require.Equal(t, originalSize, originalSizeAfter, "Original file should be unchanged")
}

func TestUPX_CompressFile_MissingInput(t *testing.T) {
    ctx := context.Background()
    upx, err := New(ctx)
    require.NoError(t, err)
    defer upx.Close(ctx)

    tempDir := t.TempDir()
    missingFile := filepath.Join(tempDir, "does_not_exist")
    outFile := filepath.Join(tempDir, "out.bin")

    err = upx.CompressFile(ctx, missingFile, outFile)
    require.Error(t, err)
    require.Contains(t, err.Error(), "input file not found")
}

func TestUPX_CompressFile_UnwritableOutput(t *testing.T) {
    ctx := context.Background()
    upx, err := New(ctx)
    require.NoError(t, err)
    defer upx.Close(ctx)

    tempDir := t.TempDir()
    testBinary := createTestBinary(t, tempDir)
    unwritable := filepath.Join(tempDir, "unwritable.bin")

    require.NoError(t, os.WriteFile(unwritable, []byte{}, 0444))

    err = upx.CompressFile(ctx, testBinary, unwritable)
    require.Error(t, err)
}


func createTestBinary(t *testing.T, dir string) string {
	src := filepath.Join(dir, "test.go")
	bin := filepath.Join(dir, "testbin")

	require.NoError(t, os.WriteFile(src, []byte(testProgram), 0644))

	cmd := exec.Command("go", "build", "-o", bin, src)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "Failed to build test binary")

	err := os.Chmod(bin, 0755)
	require.NoError(t, err, "Failed to set binary permissions")

	return bin
}

func fileSize(t *testing.T, path string) int64 {
	stat, err := os.Stat(path)
	require.NoError(t, err)
	return stat.Size()
}

const testProgram = `package main
import "fmt"
func main() { fmt.Println("UPX test binary") }`
