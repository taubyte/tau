package vault

import (
	"bytes"
	"context"
	"debug/elf"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type values map[string]string

var errDown = errors.New("the secrets service is down")

func (v values) Read(_ context.Context, label string) ([]byte, error) {
	if label == "down" {
		return nil, errDown
	}
	value, ok := v[label]
	if !ok {
		return nil, fmt.Errorf("%w: nothing here names %s", ErrNotGranted, label)
	}
	return []byte(value), nil
}

// ask is what the helper does: one GET over the socket.
func ask(t *testing.T, s *Server, label string) (int, string) {
	t.Helper()
	client := http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", filepath.Join(s.Dir(), Socket))
		},
	}}
	res, err := client.Get("http://vault/v1/secret/" + label)
	require.NoError(t, err)
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return res.StatusCode, string(body)
}

func TestAGrantedSecretIsServedAndToldFirst(t *testing.T) {
	var told []string
	s, err := Serve(values{"stripe": "sk_live_1"}, func(v []byte) { told = append(told, string(v)) })
	require.NoError(t, err)
	defer s.Close()

	status, body := ask(t, s, "stripe")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "sk_live_1", body, "unchanged: no newline added")
	require.Equal(t, []string{"sk_live_1"}, told)
}

func TestWhatIsNotGrantedIsRefusedAndNeverTold(t *testing.T) {
	told := 0
	s, err := Serve(values{}, func([]byte) { told++ })
	require.NoError(t, err)
	defer s.Close()

	status, body := ask(t, s, "stripe")
	require.Equal(t, http.StatusForbidden, status)
	require.Contains(t, body, "vault: not granted: nothing here names stripe")

	status, body = ask(t, s, "down")
	require.Equal(t, http.StatusServiceUnavailable, status)
	require.Contains(t, body, errDown.Error())

	require.Zero(t, told)
}

func TestTheMountHoldsTheSocketAndTheHelper(t *testing.T) {
	s, err := Serve(values{}, nil)
	require.NoError(t, err)

	info, err := os.Stat(filepath.Join(s.Dir(), Socket))
	require.NoError(t, err)
	require.Equal(t, os.ModeSocket, info.Mode().Type())
	require.Equal(t, os.FileMode(0o666), info.Mode().Perm(), "whoever the container runs as")

	if helperFor(runtime.GOARCH) != nil {
		info, err = os.Stat(filepath.Join(s.Dir(), Helper))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o755), info.Mode().Perm())
	}

	// The root above the mount is this user's alone.
	root, err := os.Stat(filepath.Dir(s.Dir()))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), root.Mode().Perm())

	require.NoError(t, s.Close())
	_, err = os.Stat(filepath.Dir(s.Dir()))
	require.ErrorIs(t, err, os.ErrNotExist, "closing leaves nothing on this machine")
}

func TestTheHelperIsStaticForEachMachine(t *testing.T) {
	for arch, machine := range map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64} {
		f, err := elf.NewFile(bytes.NewReader(helperFor(arch)))
		require.NoError(t, err, arch)
		require.Equal(t, machine, f.Machine, arch)
		require.Equal(t, elf.ET_EXEC, f.Type, "%s: an executable, not a shared object", arch)
		for _, p := range f.Progs {
			require.NotEqual(t, elf.PT_INTERP, p.Type, "%s: no loader, so no libc in the image", arch)
		}
		require.Less(t, len(helperFor(arch)), 64<<10, arch)
	}
	require.Nil(t, helperFor("riscv64"))
}

func TestAVaultThatCannotBeMadeLeavesNothingBehind(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	_, err := Serve(values{}, nil)
	require.Error(t, err)

	// A socket path past the kernel's limit is refused at listen, and the
	// directory made for it is removed.
	long := filepath.Join(t.TempDir(), strings.Repeat("d", 120))
	require.NoError(t, os.MkdirAll(long, 0o700))
	t.Setenv("TMPDIR", long)
	_, err = Serve(values{}, nil)
	require.Error(t, err)
	left, err := os.ReadDir(long)
	require.NoError(t, err)
	require.Empty(t, left)
}
