package ssh

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"

	"github.com/spf13/afero"
	pb "github.com/taubyte/tau/tools/spore-drive/proto/gen/mock/v1"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// command is one exec or sftp operation, with whatever arrived on its stdin.
type command struct {
	line  string
	stdin []byte
}

type hostInst struct {
	ctx  context.Context
	ctxC context.CancelFunc

	config *pb.HostConfig

	lock     sync.Mutex
	commands []command

	// fs is the host's disk: one per host, so what one connection writes the
	// next one reads, as on a real machine, and Filesystem can hand it back.
	fs afero.Fs
}

func generatePrivateKey(passphrase string) ([]byte, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	var privateKeyPEM []byte
	if passphrase != "" {
		privateKeyPEMBlock := &pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
		}
		//lint:ignore SA1019 simplest way, it's just for test
		encryptedPEMBlock, err := x509.EncryptPEMBlock(rand.Reader, privateKeyPEMBlock.Type, privateKeyPEMBlock.Bytes, []byte(passphrase), x509.PEMCipherAES256)
		if err != nil {
			return nil, err
		}
		privateKeyPEM = pem.EncodeToMemory(encryptedPEMBlock)
	} else {
		privateKeyPEM = pem.EncodeToMemory(&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
		})
	}

	return privateKeyPEM, nil
}

func newSSHServer(pctx context.Context, config *pb.HostConfig) (*hostInst, error) {
	sshConfig := &ssh.ServerConfig{}

	username := config.GetAuthUsername()
	if username == "" {
		return nil, errors.New("need to provide username")
	}

	privateKeyPEM, privateKey, err := generateServerKey(config.GetPrivateKey())
	if err != nil {
		return nil, err
	}
	config.PrivateKey = privateKeyPEM

	password := config.GetAuthPassword()
	if password != "" {
		sshConfig.PasswordCallback = func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == username && string(pass) == password {
				return nil, nil
			}
			return nil, fmt.Errorf("password rejected for %q", c.User())
		}
	}

	privKey := config.GetAuthPrivkey()
	passphrase := config.GetPassphrase()

	if privKey == nil {
		privKey, err = generatePrivateKey(passphrase)
		if err != nil {
			return nil, err
		}

		config.AuthPrivkey = privKey
	}

	var signer ssh.Signer
	if passphrase != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase(privKey, []byte(passphrase))
	} else {
		signer, err = ssh.ParsePrivateKey(privKey)
	}

	if err != nil {
		return nil, err
	}

	sshConfig.PublicKeyCallback = func(c ssh.ConnMetadata, pubKey ssh.PublicKey) (*ssh.Permissions, error) {
		if c.User() == username && bytes.Equal(signer.PublicKey().Marshal(), pubKey.Marshal()) {
			return nil, nil
		}
		return nil, fmt.Errorf("public key rejected for %q", c.User())
	}

	sshConfig.AddHostKey(privateKey)

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", config.Port))
	if err != nil {
		return nil, err
	}

	config.Port = int32(listener.Addr().(*net.TCPAddr).Port)

	hi := &hostInst{config: config, fs: afero.NewMemMapFs()}

	hi.ctx, hi.ctxC = context.WithCancel(pctx)

	go func() {
		defer hi.ctxC()
		defer listener.Close()
		for {
			select {
			case <-hi.ctx.Done():
				return
			default:
				nConn, err := listener.Accept()
				if err == nil {
					go hi.handleSSHConnection(nConn, sshConfig)
				}
			}
		}
	}()

	return hi, nil
}

func (hi *hostInst) handleSSHConnection(nConn net.Conn, config *ssh.ServerConfig) {
	conn, chans, reqs, err := ssh.NewServerConn(nConn, config)
	if err != nil {
		return
	}
	defer conn.Close()

	go ssh.DiscardRequests(reqs)

	fsHandler := &fileSystemHandler{hi: hi, fs: hi.fs}

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}

		channel, requests, err := newChannel.Accept()
		if err != nil {
			return
		}

		go func(in <-chan *ssh.Request) {
			for req := range in {
				if req.Type == "exec" {
					// The client starts streaming stdin only once exec is
					// acknowledged, and closes it before waiting for the exit.
					req.Reply(true, nil)
					stdin, err := io.ReadAll(channel)
					if err != nil {
						channel.Close()
						return
					}
					output := hi.handleCommand(string(req.Payload[4:]), stdin)
					channel.Write(output)
					channel.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
					channel.Close()
				} else if req.Type == "subsystem" && string(req.Payload[4:]) == "sftp" {
					req.Reply(true, nil)
					server := sftp.NewRequestServer(channel, sftp.Handlers{
						FileGet:  fsHandler,
						FilePut:  fsHandler,
						FileList: fsHandler,
						FileCmd:  fsHandler,
					})
					if err := server.Serve(); err != nil && !errors.Is(err, io.EOF) {
						log.Printf("SFTP server completed with error: %v", err)
					}
					channel.Close()
					return
				}
				req.Reply(false, nil)
			}
		}(requests)
	}
}

func (hi *hostInst) handleCommand(c string, stdin []byte) []byte {
	hi.lock.Lock()
	defer hi.lock.Unlock()
	hi.commands = append(hi.commands, command{line: c, stdin: stdin})
	// The deploy probes DNS resolvers with `dig +short ... @<resolver> google.com`
	// and keeps a resolver only when the first output line is a valid IP. Answer
	// that probe so the mock host looks like it can reach the public resolvers
	// (otherwise the deploy stops at "none reachable" before the stages under test).
	if strings.HasPrefix(c, "dig ") && strings.Contains(c, "google.com") {
		return []byte("142.250.190.78\n")
	}
	// A deploy uploads to /tmp over sftp and installs with `cp`, so the copy
	// is carried out on the host's disk; without it nothing a deploy installs
	// would be there to read back. Every other command only is recorded.
	if args, ok := words(c); ok {
		if len(args) > 0 && args[0] == "sudo" {
			args = args[1:]
		}
		if len(args) > 0 && args[0] == "cp" {
			if err := hi.copy(args[1:]); err != nil {
				return []byte(err.Error() + "\n")
			}
		}
	}
	return []byte("\n")
}

// words splits a command line the way the deploy quotes it: each argument a
// Go-quoted string, or a bare word.
func words(line string) ([]string, bool) {
	var out []string
	for line = strings.TrimSpace(line); line != ""; line = strings.TrimSpace(line) {
		if line[0] != '"' {
			word, rest, _ := strings.Cut(line, " ")
			out, line = append(out, word), rest
			continue
		}
		quoted, err := strconv.QuotedPrefix(line)
		if err != nil {
			return nil, false
		}
		word, err := strconv.Unquote(quoted)
		if err != nil {
			return nil, false
		}
		out, line = append(out, word), line[len(quoted):]
	}
	return out, true
}

// copy is `cp [-f] src dst` on the host's disk; a dst ending in "/" is a
// directory the file is copied into, created when missing.
func (hi *hostInst) copy(args []string) error {
	if len(args) > 0 && args[0] == "-f" {
		args = args[1:]
	}
	if len(args) != 2 {
		return fmt.Errorf("cp: want a source and a destination, got %q", args)
	}
	src, dst := args[0], args[1]
	if strings.HasSuffix(dst, "/") {
		dst = path.Join(dst, path.Base(src))
	}
	data, err := afero.ReadFile(hi.fs, src)
	if err != nil {
		return fmt.Errorf("cp: %w", err)
	}
	if err := hi.fs.MkdirAll(path.Dir(dst), 0755); err != nil {
		return fmt.Errorf("cp: %w", err)
	}
	return afero.WriteFile(hi.fs, dst, data, 0644)
}

func generateServerKey(privateKeyPEM []byte) ([]byte, ssh.Signer, error) {
	if privateKeyPEM == nil {
		privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, nil, err
		}

		privateKeyPEM = pem.EncodeToMemory(&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
		})
	}

	s, err := ssh.ParsePrivateKey(privateKeyPEM)

	return privateKeyPEM, s, err
}

type fileSystemHandler struct {
	hi *hostInst
	fs afero.Fs
}

func (h *fileSystemHandler) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	h.hi.lock.Lock()
	defer h.hi.lock.Unlock()

	h.hi.commands = append(h.hi.commands, command{line: fmt.Sprintf("SFTP Fileread %s", r.Filepath)})

	file, err := h.fs.Open(r.Filepath)
	if err != nil {
		return nil, err
	}
	return file, nil
}

func (h *fileSystemHandler) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	h.hi.lock.Lock()
	defer h.hi.lock.Unlock()

	h.hi.commands = append(h.hi.commands, command{line: fmt.Sprintf("SFTP Filewrite %s", r.Filepath)})

	file, err := h.fs.OpenFile(r.Filepath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		return nil, err
	}
	return file, nil
}

func (h *fileSystemHandler) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	h.hi.lock.Lock()
	defer h.hi.lock.Unlock()

	h.hi.commands = append(h.hi.commands, command{line: fmt.Sprintf("SFTP Filelist %s", r.Filepath)})

	files, err := afero.ReadDir(h.fs, r.Filepath)
	if err != nil {
		return nil, err
	}

	fileinfos := make([]os.FileInfo, len(files))
	copy(fileinfos, files)

	return listerAt(fileinfos), nil
}

type listerAt []os.FileInfo

func (l listerAt) ListAt(f []os.FileInfo, offset int64) (int, error) {
	if offset >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(f, l[offset:])
	if n < len(f) {
		return n, io.EOF
	}
	return n, nil
}

func (h *fileSystemHandler) Filecmd(r *sftp.Request) error {
	h.hi.lock.Lock()
	defer h.hi.lock.Unlock()

	h.hi.commands = append(h.hi.commands, command{line: fmt.Sprintf("SFTP %s %s", r.Method, r.Filepath)})

	switch r.Method {
	case "Setstat":
		return nil
	case "Rename":
		return h.fs.Rename(r.Filepath, r.Target)
	case "Rmdir":
		return h.fs.Remove(r.Filepath)
	case "Mkdir":
		return h.fs.Mkdir(r.Filepath, os.ModePerm)
	case "Remove":
		return h.fs.Remove(r.Filepath)
	default:
		return sftp.ErrSSHFxOpUnsupported
	}
}

// bundle writes every regular file on the host's disk into a tar archive, in
// path order.
func (hi *hostInst) bundle(w io.Writer) error {
	tw := tar.NewWriter(w)
	err := afero.Walk(hi.fs, "/", func(name string, info os.FileInfo, err error) error {
		if err != nil || !info.Mode().IsRegular() {
			return err
		}
		data, err := afero.ReadFile(hi.fs, name)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(data)), ModTime: info.ModTime()}); err != nil {
			return err
		}
		_, err = tw.Write(data)
		return err
	})
	if err != nil {
		return err
	}
	return tw.Close()
}
