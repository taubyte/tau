package service

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/spf13/afero/zipfs"
	"github.com/taubyte/tau/pkg/spore-drive/config"
	"github.com/taubyte/tau/pkg/spore-drive/config/fixtures"
	pb "github.com/taubyte/tau/pkg/spore-drive/proto/gen/config/v1"
	pbconnect "github.com/taubyte/tau/pkg/spore-drive/proto/gen/config/v1/configv1connect"
	"go4.org/readerutil"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"gotest.tools/v3/assert"
)

func TestFilesystemFromBundle_InvalidType(t *testing.T) {
	bundle := []byte("invalid data")
	_, err := filesystemFromBundle(bundle, "/")
	assert.ErrorContains(t, err, "bundle format unsupported")
}

func TestFilesystemFromBundle_ValidZip(t *testing.T) {
	fs, _ := fixtures.VirtConfig()
	var buf bytes.Buffer
	err := zipFilesystem(context.Background(), fs, &buf)
	assert.NilError(t, err)
	bundleData := buf.Bytes()

	fsResult, err := filesystemFromBundle(bundleData, "/")
	assert.NilError(t, err)
	assert.Equal(t, fsResult != nil, true)
}

func TestFilesystemFromBundle_ValidTar(t *testing.T) {
	fs, _ := fixtures.VirtConfig()
	var buf bytes.Buffer
	err := tarFilesystem(context.Background(), fs, &buf)
	assert.NilError(t, err)
	bundleData := buf.Bytes()

	fsResult, err := filesystemFromBundle(bundleData, "/")
	assert.NilError(t, err)
	assert.Equal(t, fsResult != nil, true)
}

func newTestServer(t *testing.T) (*Service, string) {
	// Create a new listener on a dynamic port (":0" means any available port)
	listener, err := net.Listen("tcp", ":0")
	assert.NilError(t, err)

	// Get the dynamically assigned port
	port := listener.Addr().(*net.TCPAddr).Port

	svr, err := Serve()
	assert.NilError(t, err)

	mux := http.NewServeMux()
	svr.Attach(mux)
	go func() {
		defer listener.Close()
		http.Serve(
			listener,
			// Use h2c so we can serve HTTP/2 without TLS.
			h2c.NewHandler(mux, &http2.Server{}),
		)
	}()

	return svr, fmt.Sprintf("http://localhost:%d/", port)
}

func findGoModDir(t *testing.T) string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not get the caller information")
	}

	// Start from the directory of the current file
	dir := filepath.Dir(filename)

	// Walk up the directory structure until we find go.mod
	for {
		modPath := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(modPath); err == nil {
			// go.mod file found
			return dir
		}

		// Move one level up
		parentDir := filepath.Dir(dir)
		if parentDir == dir {
			// We've reached the root without finding go.mod
			break
		}
		dir = parentDir
	}

	t.Fatal("go.mod file not found")
	return ""
}

func copyToTmpDir(t *testing.T, src string) string {
	tmpDir, err := os.MkdirTemp("", t.Name()+"_cnf")
	assert.NilError(t, err, "failed to create temp directory")

	err = filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		dstPath := filepath.Join(tmpDir, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		dstFile, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		defer dstFile.Close()

		_, err = io.Copy(dstFile, srcFile)
		return err
	})

	assert.NilError(t, err, "failed to copy directory")

	return tmpDir
}

func TestServerLoadAndDo(t *testing.T) {
	svr, addr := newTestServer(t)
	client := pbconnect.NewConfigServiceClient(http.DefaultClient, addr)

	cnfDir := copyToTmpDir(t, findGoModDir(t)+"/pkg/spore-drive/config/fixtures/config")
	defer os.RemoveAll(cnfDir)

	res, err := client.Load(context.Background(), connect.NewRequest[pb.Source](
		&pb.Source{
			Root: cnfDir,
			Path: "/",
		},
	))

	assert.NilError(t, err)

	confId := res.Msg.GetId()

	assert.Equal(t, svr.configs[confId] != nil, true)

	req := connect.NewRequest(&pb.Op{
		Config: &pb.Config{
			Id: confId,
		},
		Op: &pb.Op_Cloud{
			Cloud: &pb.Cloud{
				Op: &pb.Cloud_Domain{
					Domain: &pb.Domain{
						Op: &pb.Domain_Root{
							Root: &pb.StringOp{
								Op: &pb.StringOp_Get{Get: true},
							},
						},
					},
				},
			},
		},
	})

	cres, err := client.Do(context.Background(), req)
	assert.NilError(t, err)
	assert.Equal(t, cres.Msg.GetString_(), "test.com")

	_, err = client.Do(context.Background(), connect.NewRequest(&pb.Op{
		Config: &pb.Config{
			Id: confId,
		},
		Op: &pb.Op_Cloud{
			Cloud: &pb.Cloud{
				Op: &pb.Cloud_Domain{
					Domain: &pb.Domain{
						Op: &pb.Domain_Root{
							Root: &pb.StringOp{
								Op: &pb.StringOp_Set{
									Set: "test2.com",
								},
							},
						},
					},
				},
			},
		},
	}))
	assert.NilError(t, err)

	cres, err = client.Do(context.Background(), req)
	assert.NilError(t, err)
	assert.Equal(t, cres.Msg.GetString_(), "test2.com")
}

func TestServerUploadAndDoThenCommit(t *testing.T) {
	fs, _ := fixtures.VirtConfig()
	var tarCnf bytes.Buffer
	err := tarFilesystem(context.Background(), fs, &tarCnf)
	assert.NilError(t, err)

	svr, addr := newTestServer(t)
	client := pbconnect.NewConfigServiceClient(http.DefaultClient, addr)

	stream := client.Upload(context.Background())

	buf := make([]byte, 1024)
	for {
		n, err := tarCnf.Read(buf)
		if n > 0 {
			assert.NilError(t, stream.Send(&pb.SourceUpload{Data: &pb.SourceUpload_Chunk{Chunk: buf[:n]}}))
		}
		if err == io.EOF {
			break
		}
		assert.NilError(t, err)
	}
	assert.NilError(t, stream.Send(&pb.SourceUpload{Data: &pb.SourceUpload_Path{Path: "/"}}))

	res, err := stream.CloseAndReceive()
	assert.NilError(t, err)

	confId := res.Msg.GetId()
	cnf := svr.configs[confId]

	assert.Equal(t, cnf != nil, true)

	req := connect.NewRequest(&pb.Op{
		Config: &pb.Config{
			Id: confId,
		},
		Op: &pb.Op_Cloud{
			Cloud: &pb.Cloud{
				Op: &pb.Cloud_Domain{
					Domain: &pb.Domain{
						Op: &pb.Domain_Root{
							Root: &pb.StringOp{
								Op: &pb.StringOp_Get{Get: true},
							},
						},
					},
				},
			},
		},
	})

	cres, err := client.Do(context.Background(), req)
	assert.NilError(t, err)
	assert.Equal(t, cres.Msg.GetString_(), "test.com")

	_, err = client.Do(context.Background(), connect.NewRequest(&pb.Op{
		Config: &pb.Config{
			Id: confId,
		},
		Op: &pb.Op_Cloud{
			Cloud: &pb.Cloud{
				Op: &pb.Cloud_Domain{
					Domain: &pb.Domain{
						Op: &pb.Domain_Root{
							Root: &pb.StringOp{
								Op: &pb.StringOp_Set{
									Set: "test2.com",
								},
							},
						},
					},
				},
			},
		},
	}))
	assert.NilError(t, err)

	cres, err = client.Do(context.Background(), req)
	assert.NilError(t, err)
	assert.Equal(t, cres.Msg.GetString_(), "test2.com")

	_, err = client.Commit(context.Background(), connect.NewRequest(&pb.Config{Id: confId}))
	assert.NilError(t, err)

	dstream, err := client.Download(context.Background(), connect.NewRequest(&pb.BundleConfig{Id: &pb.Config{Id: confId}, Type: pb.BundleType_BUNDLE_ZIP}))
	assert.NilError(t, err)

	var dbuf bytes.Buffer

	for dstream.Receive() {
		chunk := dstream.Msg().GetChunk()
		if chunk != nil {
			dbuf.Write(chunk)
		} else {
			assert.Equal(t, dstream.Msg().GetType(), pb.BundleType_BUNDLE_ZIP)
		}
	}

	assert.Equal(t, dbuf.Len() > 2500, true)

	zbun, err := zip.NewReader(readerutil.NewBufferingReaderAt(&dbuf), int64(dbuf.Len()))
	assert.NilError(t, err)
	zfs := zipfs.New(zbun)

	p, err := config.New(zfs, "/")
	assert.NilError(t, err)

	assert.Equal(t, p.Cloud().Domain().Root(), "test2.com")
}

func TestDoConcurrentAccess(t *testing.T) {
	_, addr := newTestServer(t)
	client := pbconnect.NewConfigServiceClient(http.DefaultClient, addr)

	// Load configuration
	cnfDir := copyToTmpDir(t, findGoModDir(t)+"/pkg/spore-drive/config/fixtures/config")
	defer os.RemoveAll(cnfDir)

	res, err := client.Load(context.Background(), connect.NewRequest[pb.Source](
		&pb.Source{
			Root: cnfDir,
			Path: "/",
		},
	))
	assert.NilError(t, err)
	confId := res.Msg.GetId()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := connect.NewRequest(&pb.Op{
				Config: &pb.Config{Id: confId},
				Op: &pb.Op_Cloud{
					Cloud: &pb.Cloud{
						Op: &pb.Cloud_Domain{
							Domain: &pb.Domain{
								Op: &pb.Domain_Root{
									Root: &pb.StringOp{
										Op: &pb.StringOp_Get{Get: true},
									},
								},
							},
						},
					},
				},
			})
			_, err := client.Do(context.Background(), req)
			assert.NilError(t, err)
		}()
	}
	wg.Wait()
}
