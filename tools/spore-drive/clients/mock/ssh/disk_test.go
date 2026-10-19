package ssh_test

import (
	"context"
	"fmt"
	"testing"

	"connectrpc.com/connect"
	"github.com/pkg/sftp"
	gossh "golang.org/x/crypto/ssh"
	"gotest.tools/v3/assert"

	pb "github.com/taubyte/tau/tools/spore-drive/proto/gen/mock/v1"
)

func dial(t *testing.T, port int32) *gossh.Client {
	t.Helper()
	c, err := gossh.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port), &gossh.ClientConfig{
		User:            "tau",
		Auth:            []gossh.AuthMethod{gossh.Password("testtest")},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
	})
	assert.NilError(t, err)
	t.Cleanup(func() { c.Close() })
	return c
}

func run(t *testing.T, c *gossh.Client, cmd string) string {
	t.Helper()
	s, err := c.NewSession()
	assert.NilError(t, err)
	defer s.Close()
	out, err := s.CombinedOutput(cmd)
	assert.NilError(t, err)
	return string(out)
}

// TestAHostsDiskOutlivesAConnection: what one connection writes, the next
// reads, and `cp` is carried out on it.
func TestAHostsDiskOutlivesAConnection(t *testing.T) {
	_, m := mock(t)
	h, err := m.New(context.Background(), connect.NewRequest(&pb.HostConfig{Host: &pb.Host{Name: "disk"}, AuthUsername: "tau", AuthPassword: "testtest"}))
	assert.NilError(t, err)
	port := h.Msg.GetPort()

	first := dial(t, port)
	fs, err := sftp.NewClient(first)
	assert.NilError(t, err)
	f, err := fs.Create("/tmp/a.yaml")
	assert.NilError(t, err)
	_, err = f.Write([]byte("hello"))
	assert.NilError(t, err)
	assert.NilError(t, f.Close())
	first.Close()

	second := dial(t, port)
	assert.Equal(t, run(t, second, `sudo "cp" "-f" "/tmp/a.yaml" "/tb/config/"`), "\n")
	assert.Equal(t, run(t, second, `cp /tmp/a.yaml /tb/b.yaml`), "\n")
	assert.Equal(t, run(t, second, `sudo "cp" "-f" "/tmp/a.yaml"`), "cp: want a source and a destination, got [\"/tmp/a.yaml\"]\n")
	assert.Assert(t, run(t, second, `cp /tmp/missing /tb/`) != "\n")
	assert.Equal(t, run(t, second, `cp "unterminated`), "\n", "a line that does not split is only recorded")

	disk := files(t, m, "disk")
	assert.DeepEqual(t, disk, map[string][]byte{
		"/tmp/a.yaml":       []byte("hello"),
		"/tb/config/a.yaml": []byte("hello"),
		"/tb/b.yaml":        []byte("hello"),
	})
}
