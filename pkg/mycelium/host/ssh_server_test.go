package host

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"gotest.tools/v3/assert"
)

func generatePrivateKey(t *testing.T, passphrase string) (string, ssh.Signer) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	assert.NilError(t, err, "Failed to generate private key")

	var privateKeyPEM []byte
	if passphrase != "" {
		privateKeyPEMBlock := &pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
		}
		//lint:ignore SA1019 simplest way, it's just for test
		encryptedPEMBlock, err := x509.EncryptPEMBlock(rand.Reader, privateKeyPEMBlock.Type, privateKeyPEMBlock.Bytes, []byte(passphrase), x509.PEMCipherAES256)
		assert.NilError(t, err, "Failed to encrypt private key with passphrase")
		privateKeyPEM = pem.EncodeToMemory(encryptedPEMBlock)
	} else {
		privateKeyPEM = pem.EncodeToMemory(&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
		})
	}

	var signer ssh.Signer
	if passphrase != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase(privateKeyPEM, []byte(passphrase))
		assert.NilError(t, err, "Failed to parse private key with passphrase")
	} else {
		signer, err = ssh.ParsePrivateKey(privateKeyPEM)
		assert.NilError(t, err, "Failed to parse private key")
	}

	return string(privateKeyPEM), signer
}

func newSSHServer(t *testing.T, port string) {
	privateKey, err := generateServerKey()
	assert.NilError(t, err, "Failed to generate server key")

	config := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == "testuser" && string(pass) == "password" {
				return nil, nil
			}
			return nil, fmt.Errorf("password rejected for %q", c.User())
		},
		PublicKeyCallback: func(c ssh.ConnMetadata, pubKey ssh.PublicKey) (*ssh.Permissions, error) {
			authorizedKey := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pubKey)))
			expectedKey := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pubKey)))
			if authorizedKey == expectedKey {
				return nil, nil
			}
			return nil, fmt.Errorf("public key rejected for %q", c.User())
		},
	}

	config.AddHostKey(privateKey)

	listener, err := net.Listen("tcp", "127.0.0.1:"+port)
	assert.NilError(t, err, "Failed to listen for connection")

	go func() {
		for {
			nConn, err := listener.Accept()
			if err != nil {
				log.Println("Failed to accept incoming connection: ", err)
				continue
			}

			go handleSSHConnection(nConn, config)
		}
	}()

	time.Sleep(500 * time.Millisecond)
}

func handleSSHConnection(nConn net.Conn, config *ssh.ServerConfig) {
	conn, chans, reqs, err := ssh.NewServerConn(nConn, config)
	if err != nil {
		log.Println("Failed to establish SSH connection: ", err)
		return
	}
	defer conn.Close()

	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}

		channel, requests, err := newChannel.Accept()
		if err != nil {
			log.Printf("Could not accept channel: %v", err)
			return
		}

		go func(in <-chan *ssh.Request) {
			for req := range in {
				if req.Type == "exec" {
					cmd := string(req.Payload[4:])
					output := handleCommand(cmd)
					channel.Write(output)
					channel.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
					req.Reply(true, nil)
					channel.Close()
				}
			}
		}(requests)
	}
}

func handleCommand(cmd string) []byte {
	switch cmd {
	case "echo \"hello\"":
		return []byte("hello\n")
	default:
		return []byte(fmt.Sprintf("command not found: %s\n", cmd))
	}
}

func generateServerKey() (ssh.Signer, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})
	return ssh.ParsePrivateKey(privateKeyPEM)
}

func newSFTPServer(t *testing.T, port string) {
	privateKey, err := generateServerKey()
	assert.NilError(t, err, "Failed to generate server key")

	config := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == "testuser" && string(pass) == "password" {
				return nil, nil
			}
			return nil, fmt.Errorf("password rejected for %q", c.User())
		},
	}

	config.AddHostKey(privateKey)

	listener, err := net.Listen("tcp", "127.0.0.1:"+port)
	assert.NilError(t, err, "Failed to listen for connection")

	go func() {
		for {
			nConn, err := listener.Accept()
			if err != nil {
				log.Println("Failed to accept incoming connection: ", err)
				continue
			}

			go handleSFTPConnection(nConn, config)
		}
	}()

	time.Sleep(500 * time.Millisecond)
}

func handleSFTPConnection(nConn net.Conn, config *ssh.ServerConfig) {
	conn, chans, reqs, err := ssh.NewServerConn(nConn, config)
	if err != nil {
		log.Println("Failed to establish SSH connection: ", err)
		return
	}
	defer conn.Close()

	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}

		channel, requests, err := newChannel.Accept()
		if err != nil {
			log.Printf("Could not accept channel: %v", err)
			return
		}

		go func(in <-chan *ssh.Request) {
			for req := range in {
				if req.Type == "subsystem" && string(req.Payload[4:]) == "sftp" {
					req.Reply(true, nil)
					server, err := sftp.NewServer(channel)
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
