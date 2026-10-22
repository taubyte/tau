package ssh_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"connectrpc.com/connect"
	"github.com/pkg/sftp"
	gossh "golang.org/x/crypto/ssh"
	"gotest.tools/v3/assert"

	pb "github.com/taubyte/tau/tools/spore-drive/proto/gen/mock/v1"
	"github.com/taubyte/tau/tools/spore-drive/proto/gen/mock/v1/mockv1connect"
)

func commands(t *testing.T, m mockv1connect.MockSSHServiceClient, host string) []*pb.Command {
	t.Helper()
	stream, err := m.Commands(context.Background(), connect.NewRequest(&pb.Host{Name: host}))
	assert.NilError(t, err)
	var out []*pb.Command
	for stream.Receive() {
		out = append(out, stream.Msg())
	}
	assert.NilError(t, stream.Err())
	return out
}

// TestACommandsStdinIsRecorded: what a command reads on stdin is recorded
// with it, byte for byte, and a command handed none records none.
func TestACommandsStdinIsRecorded(t *testing.T) {
	_, m := mock(t)
	h, err := m.New(context.Background(), connect.NewRequest(&pb.HostConfig{Host: &pb.Host{Name: "stdin"}, AuthUsername: "tau", AuthPassword: "testtest"}))
	assert.NilError(t, err)
	c := dial(t, h.Msg.GetPort())

	payload := append([]byte{0, 1, 2, 0xff}, bytes.Repeat([]byte("sealed"), 50000)...)
	s, err := c.NewSession()
	assert.NilError(t, err)
	s.Stdin = bytes.NewReader(payload)
	out, err := s.CombinedOutput(`sudo "/tb/bin/tau" "init" "-"`)
	assert.NilError(t, err)
	assert.Equal(t, string(out), "\n")
	s.Close()
	assert.Equal(t, run(t, c, "true"), "\n")

	cmds := commands(t, m, "stdin")
	assert.Equal(t, len(cmds), 2)
	assert.Equal(t, cmds[0].GetCommand(), `sudo "/tb/bin/tau" "init" "-"`)
	assert.DeepEqual(t, cmds[0].GetStdin(), payload)
	assert.Equal(t, cmds[1].GetIndex(), int32(1))
	assert.Equal(t, len(cmds[1].GetStdin()), 0)
}

func TestHostsAreLookedUpAndFreed(t *testing.T) {
	_, m := mock(t)
	ctx := context.Background()

	_, err := m.New(ctx, connect.NewRequest(&pb.HostConfig{Host: &pb.Host{Name: "x"}}))
	assert.ErrorContains(t, err, "username")

	h, err := m.New(ctx, connect.NewRequest(&pb.HostConfig{AuthUsername: "tau", AuthPassword: "testtest"}))
	assert.NilError(t, err)
	name := h.Msg.GetHost().GetName()
	assert.Assert(t, name != "", "a host without a name is given one")

	_, err = m.New(ctx, connect.NewRequest(&pb.HostConfig{Host: &pb.Host{Name: name}, AuthUsername: "tau"}))
	assert.ErrorContains(t, err, "host exists")

	_, err = m.New(ctx, connect.NewRequest(&pb.HostConfig{Host: &pb.Host{Name: "bad"}, AuthUsername: "tau", AuthPrivkey: []byte("not a key")}))
	assert.Assert(t, err != nil)
	_, err = m.New(ctx, connect.NewRequest(&pb.HostConfig{Host: &pb.Host{Name: "bad"}, AuthUsername: "tau", PrivateKey: []byte("not a key")}))
	assert.Assert(t, err != nil)

	byName, err := m.Lookup(ctx, connect.NewRequest(&pb.Query{Eq: &pb.Query_Name{Name: name}}))
	assert.NilError(t, err)
	assert.Equal(t, byName.Msg.GetPort(), h.Msg.GetPort())
	byPort, err := m.Lookup(ctx, connect.NewRequest(&pb.Query{Eq: &pb.Query_Port{Port: h.Msg.GetPort()}}))
	assert.NilError(t, err)
	assert.Equal(t, byPort.Msg.GetHost().GetName(), name)
	for _, q := range []*pb.Query{{Eq: &pb.Query_Name{Name: "nobody"}}, {Eq: &pb.Query_Port{Port: 1}}, {}} {
		_, err := m.Lookup(ctx, connect.NewRequest(q))
		assert.Assert(t, err != nil, "%v", q)
	}

	for _, host := range []string{"", "nobody"} {
		_, err := m.Free(ctx, connect.NewRequest(&pb.Host{Name: host}))
		assert.Assert(t, err != nil)
		stream, err := m.Commands(ctx, connect.NewRequest(&pb.Host{Name: host}))
		assert.NilError(t, err)
		assert.Assert(t, !stream.Receive())
		assert.Assert(t, stream.Err() != nil)
	}
	_, err = m.Free(ctx, connect.NewRequest(&pb.Host{Name: name}))
	assert.NilError(t, err)
}

// TestAHostAcceptsOnlyItsOwnCredentials: the key it generated for a user,
// with or without a passphrase, and the user's password.
func TestAHostAcceptsOnlyItsOwnCredentials(t *testing.T) {
	_, m := mock(t)
	ctx := context.Background()

	for _, passphrase := range []string{"", "open sesame"} {
		h, err := m.New(ctx, connect.NewRequest(&pb.HostConfig{AuthUsername: "tau", AuthPassword: "pw", Passphrase: passphrase}))
		assert.NilError(t, err)

		var signer gossh.Signer
		if passphrase == "" {
			signer, err = gossh.ParsePrivateKey(h.Msg.GetAuthPrivkey())
		} else {
			signer, err = gossh.ParsePrivateKeyWithPassphrase(h.Msg.GetAuthPrivkey(), []byte(passphrase))
		}
		assert.NilError(t, err)

		addr := fmt.Sprintf("127.0.0.1:%d", h.Msg.GetPort())
		connect := func(user string, auth gossh.AuthMethod) error {
			c, err := gossh.Dial("tcp", addr, &gossh.ClientConfig{User: user, Auth: []gossh.AuthMethod{auth}, HostKeyCallback: gossh.InsecureIgnoreHostKey()})
			if err == nil {
				c.Close()
			}
			return err
		}
		assert.NilError(t, connect("tau", gossh.PublicKeys(signer)))
		assert.Assert(t, connect("other", gossh.PublicKeys(signer)) != nil)
		assert.Assert(t, connect("tau", gossh.Password("wrong")) != nil)

		other, err := gossh.ParsePrivateKey(h.Msg.GetPrivateKey())
		assert.NilError(t, err)
		assert.Assert(t, connect("tau", gossh.PublicKeys(other)) != nil)
	}
}

// TestAHostRefusesWhatItDoesNotServe: only sessions, and in them only exec
// and sftp.
func TestAHostRefusesWhatItDoesNotServe(t *testing.T) {
	_, m := mock(t)
	h, err := m.New(context.Background(), connect.NewRequest(&pb.HostConfig{Host: &pb.Host{Name: "refuse"}, AuthUsername: "tau", AuthPassword: "testtest"}))
	assert.NilError(t, err)
	c := dial(t, h.Msg.GetPort())

	_, _, err = c.OpenChannel("direct-tcpip", nil)
	assert.Assert(t, err != nil)

	s, err := c.NewSession()
	assert.NilError(t, err)
	defer s.Close()
	assert.Assert(t, s.RequestPty("xterm", 80, 24, gossh.TerminalModes{}) != nil)
}

func TestSFTPCommandsActOnTheHostsDisk(t *testing.T) {
	_, m := mock(t)
	h, err := m.New(context.Background(), connect.NewRequest(&pb.HostConfig{Host: &pb.Host{Name: "sftp"}, AuthUsername: "tau", AuthPassword: "testtest"}))
	assert.NilError(t, err)
	fs, err := sftp.NewClient(dial(t, h.Msg.GetPort()))
	assert.NilError(t, err)
	defer fs.Close()

	assert.NilError(t, fs.Mkdir("/d"))
	for _, name := range []string{"/d/a", "/d/b", "/d/c"} {
		f, err := fs.Create(name)
		assert.NilError(t, err)
		f.Write([]byte(name))
		assert.NilError(t, f.Close())
	}
	assert.NilError(t, fs.Chmod("/d/a", 0600))
	assert.NilError(t, fs.Rename("/d/a", "/d/z"))
	assert.NilError(t, fs.Remove("/d/b"))

	entries, err := fs.ReadDir("/d")
	assert.NilError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	assert.DeepEqual(t, names, []string{"c", "z"})
	_, err = fs.ReadDir("/missing")
	assert.Assert(t, err != nil)

	f, err := fs.Open("/d/z")
	assert.NilError(t, err)
	data := make([]byte, 4)
	n, err := f.Read(data)
	assert.NilError(t, err)
	assert.Equal(t, string(data[:n]), "/d/a")
	f.Close()
	_, err = fs.Open("/missing")
	assert.Assert(t, err != nil)

	assert.Assert(t, fs.Link("/d/c", "/d/l") != nil, "unsupported")
	assert.NilError(t, fs.Remove("/d/c"))
	assert.NilError(t, fs.Remove("/d/z"))
	assert.NilError(t, fs.RemoveDirectory("/d"))

	var lines []string
	for _, c := range commands(t, m, "sftp") {
		lines = append(lines, c.GetCommand())
	}
	assert.Assert(t, len(lines) > 0)
	assert.Assert(t, contains(lines, "SFTP Filelist /d"), "%q", lines)
	assert.Assert(t, contains(lines, "SFTP Rmdir /d"), "%q", lines)
}

func contains(lines []string, line string) bool {
	for _, l := range lines {
		if l == line {
			return true
		}
	}
	return false
}
