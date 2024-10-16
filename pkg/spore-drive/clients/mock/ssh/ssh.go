package ssh

import (
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
	"sync"

	"github.com/spf13/afero"
	pb "github.com/taubyte/tau/pkg/spore-drive/proto/gen/mock/v1"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type hostInst struct {
	ctx  context.Context
	ctxC context.CancelFunc

	config *pb.HostConfig

	lock     sync.Mutex
	commands []string
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

	hi := &hostInst{config: config}

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

	fsHandler := &fileSystemHandler{hi: hi, fs: afero.NewMemMapFs()}

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
					cmd := string(req.Payload[4:])
					output := hi.handleCommand(cmd)
					channel.Write(output)
					channel.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
					req.Reply(true, nil)
					channel.Close()
				} else if req.Type == "subsystem" && string(req.Payload[4:]) == "sftp" {
					req.Reply(true, nil)
					server := sftp.NewRequestServer(channel, sftp.Handlers{
						FileGet:  fsHandler,
						FilePut:  fsHandler,
						FileList: fsHandler,
						FileCmd:  fsHandler,
					})
					if err != nil {
						log.Printf("SFTP server creation error: %v", err)
						return
					}
					if err := server.Serve(); err != nil {
						log.Printf("SFTP server completed with error: %v", err)
					} else {
						log.Println("SFTP server completed successfully")
					}
					return
				}
				req.Reply(false, nil)
			}
		}(requests)
	}
}

func (hi *hostInst) handleCommand(c string) []byte {
	hi.lock.Lock()
	defer hi.lock.Unlock()
	hi.commands = append(hi.commands, c)
	return []byte("\n")
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

	h.hi.commands = append(h.hi.commands, fmt.Sprintf("SFTP Fileread %s", r.Filepath))

	file, err := h.fs.Open(r.Filepath)
	if err != nil {
		return nil, err
	}
	return file, nil
}

func (h *fileSystemHandler) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	h.hi.lock.Lock()
	defer h.hi.lock.Unlock()

	h.hi.commands = append(h.hi.commands, fmt.Sprintf("SFTP Filewrite %s", r.Filepath))

	file, err := h.fs.OpenFile(r.Filepath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		return nil, err
	}
	return file, nil
}

func (h *fileSystemHandler) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	h.hi.lock.Lock()
	defer h.hi.lock.Unlock()

	h.hi.commands = append(h.hi.commands, fmt.Sprintf("SFTP Filelist %s", r.Filepath))

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

	h.hi.commands = append(h.hi.commands, fmt.Sprintf("SFTP %s %s", r.Method, r.Filepath))

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
