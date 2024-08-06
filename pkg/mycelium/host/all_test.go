package host

import (
	"context"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/taubyte/tau/pkg/mycelium/command"
	"golang.org/x/crypto/ssh"
	"gotest.tools/v3/assert"
)

func TestHostCommandExecution(t *testing.T) {
	port := "2222"
	newSSHServer(t, port)

	username := "testuser"
	password := "password"
	address := "127.0.0.1:" + port
	timeout := 5 * time.Second

	h, err := New(
		Address(address),
		Timeout(timeout),
		Password(username, password),
	)
	assert.NilError(t, err, "Host creation failed")

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd, err := h.Command(ctx, "echo", command.Args("hello"))
	assert.NilError(t, err, "Command creation failed")

	output, err := cmd.CombinedOutput()
	assert.NilError(t, err, "Command execution failed")
	assert.Equal(t, "hello\n", string(output), "Unexpected command output")
}

func TestHostAttributes(t *testing.T) {
	port := "2223"
	newSSHServer(t, port)

	h := &host{}
	err := Address("127.0.0.1:" + port)(h)
	assert.NilError(t, err, "Failed to set address")
	assert.Equal(t, h.addr, "127.0.0.1")
	assert.Equal(t, h.port, uint64(2223))

	err = Port(2223)(h)
	assert.NilError(t, err, "Failed to set port")
	assert.Equal(t, h.port, uint64(2223))

	err = Timeout(5 * time.Second)(h)
	assert.NilError(t, err, "Failed to set timeout")
	assert.Equal(t, h.timeout, 5*time.Second)
}

func TestHostAuthAttributes(t *testing.T) {
	port := "2224"
	newSSHServer(t, port)

	privateKey, _ := generatePrivateKey(t, "")
	privateKeyReader := strings.NewReader(privateKey)

	h, err := New(
		Address("127.0.0.1:"+port),
		Password("testuser", "password"),
		Key("testuser", "", privateKeyReader),
	)
	assert.NilError(t, err, "Failed to create Host with key authentication")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd, err := h.Command(ctx, "echo", command.Args("hello"))
	assert.NilError(t, err, "Command creation failed")

	output, err := cmd.CombinedOutput()
	assert.NilError(t, err, "Command execution failed")
	assert.Equal(t, "hello\n", string(output), "Unexpected command output")
}

func TestHostPublicKey(t *testing.T) {
	port := "2225"
	newSSHServer(t, port)

	_, signer := generatePrivateKey(t, "")
	publicKey := signer.PublicKey()
	publicKeyReader := strings.NewReader(string(ssh.MarshalAuthorizedKey(publicKey)))

	hi, err := New(
		Address("127.0.0.1:"+port),
		PublicKey(publicKeyReader),
	)

	h := hi.(*host)

	assert.NilError(t, err, "Failed to create Host with public key")

	assert.DeepEqual(t, h.key.Marshal(), publicKey.Marshal())
}

func TestNewHostWithInvalidAddress(t *testing.T) {
	_, err := New(Address("1.2.3.4:70000"))
	assert.Error(t, err, "applying attribute: parsing port: strconv.ParseUint: parsing \"70000\": value out of range")
}

func TestHostCommandExecutionTimeout(t *testing.T) {
	port := "2226"
	newSSHServer(t, port)

	username := "testuser"
	password := "password"
	address := "127.0.0.1:" + port
	timeout := 1 * time.Second

	h, err := New(
		Address(address),
		Timeout(timeout),
		Password(username, password),
	)
	assert.NilError(t, err, "Host creation failed")

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd, err := h.Command(ctx, "echo", command.Args("hello"))
	assert.NilError(t, err, "Command creation failed")

	// Simulate a long-running command by sleeping for more than the context timeout
	time.Sleep(2 * time.Second)

	_, err = cmd.CombinedOutput()
	assert.Error(t, err, "context done: context deadline exceeded")
}

func TestHostInvalidAuth(t *testing.T) {
	port := "2227"
	newSSHServer(t, port)

	// Create Host with invalid authentication
	h, err := New(
		Address("127.0.0.1:"+port),
		Password("wronguser", "wrongpassword"),
	)
	assert.NilError(t, err, "Host creation failed")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = h.Command(ctx, "echo", command.Args("hello"))
	assert.Error(t, err, "creating new session: initializing host: failed to initialize SSH client: ssh: handshake failed: ssh: unable to authenticate, attempted methods [none password], no supported methods remain")
}

func TestHostWithMultipleAuthMethods(t *testing.T) {
	port := "2228"
	newSSHServer(t, port)

	privateKey, _ := generatePrivateKey(t, "")
	privateKeyReader := strings.NewReader(privateKey)

	// Create Host with both password and key authentication
	h, err := New(
		Address("127.0.0.1:"+port),
		Password("testuser", "password"),
		Key("testuser", "", privateKeyReader),
	)
	assert.NilError(t, err, "Failed to create Host with multiple authentication methods")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd, err := h.Command(ctx, "echo", command.Args("hello"))
	assert.NilError(t, err, "Command creation failed")

	output, err := cmd.CombinedOutput()
	assert.NilError(t, err, "Command execution failed")
	assert.Equal(t, "hello\n", string(output), "Unexpected command output")
}

func TestHostWithKeyPassphrase(t *testing.T) {
	port := "2229"
	newSSHServer(t, port)

	privateKey, _ := generatePrivateKey(t, "testpassphrase")
	privateKeyReader := strings.NewReader(privateKey)

	// Create Host with key authentication with passphrase
	h, err := New(
		Address("127.0.0.1:"+port),
		Key("testuser", "testpassphrase", privateKeyReader),
	)
	assert.NilError(t, err, "Failed to create Host with key passphrase")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd, err := h.Command(ctx, "echo", command.Args("hello"))
	assert.NilError(t, err, "Command creation failed")

	output, err := cmd.CombinedOutput()
	assert.NilError(t, err, "Command execution failed")
	assert.Equal(t, "hello\n", string(output), "Unexpected command output")
}

func TestHostFs(t *testing.T) {
	port := "2230"
	newSFTPServer(t, port)

	username := "testuser"
	password := "password"
	address := "127.0.0.1:" + port
	timeout := 5 * time.Second

	h, err := New(
		Address(address),
		Timeout(timeout),
		Password(username, password),
	)
	assert.NilError(t, err, "Host creation failed")

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	tempDir := t.TempDir()

	fs, err := h.Fs(ctx)
	assert.NilError(t, err, "Filesystem creation failed")

	file, err := fs.Create(path.Join(tempDir, "testfile"))
	assert.NilError(t, err, "File creation failed")

	_, err = file.Write([]byte("hello world"))
	assert.NilError(t, err, "File write failed")

	file.Close()

	readFile, err := fs.Open(path.Join(tempDir, "testfile"))
	assert.NilError(t, err, "File open failed")

	content := make([]byte, 11)
	_, err = readFile.Read(content)
	assert.NilError(t, err, "File read failed")
	assert.Equal(t, "hello world", string(content), "Unexpected file content")
}

func TestHostClone(t *testing.T) {
	port := "2231"
	newSSHServer(t, port)

	originalHost, err := New(
		Address("127.0.0.1:"+port),
		Password("testuser", "password"),
	)
	assert.NilError(t, err, "Failed to create original Host")

	// Clone the original host with additional attributes
	clonedHost, err := originalHost.Clone(
		Timeout(10 * time.Second),
	)
	assert.NilError(t, err, "Failed to clone Host")

	assert.Equal(t, originalHost.(*host).addr, clonedHost.(*host).addr)
	assert.Equal(t, originalHost.(*host).port, clonedHost.(*host).port)
	assert.Equal(t, len(originalHost.(*host).auth), len(clonedHost.(*host).auth))
	assert.Equal(t, clonedHost.(*host).timeout, 10*time.Second)
}

func TestHostString(t *testing.T) {
	h, err := New(
		Address("127.0.0.1:2232"),
	)
	assert.NilError(t, err, "Failed to create Host")

	expected := "127.0.0.1:2232"
	actual := h.String()

	assert.Equal(t, expected, actual, "Host string representation is incorrect")
}
