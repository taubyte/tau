package service

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"sync"

	"connectrpc.com/connect"
	"github.com/h2non/filetype"
	"github.com/h2non/filetype/matchers"
	"github.com/spf13/afero"
	"github.com/spf13/afero/tarfs"
	"github.com/spf13/afero/zipfs"
	pb "github.com/taubyte/tau/pkg/spore-drive/proto/gen/config/v1"
	pbconnect "github.com/taubyte/tau/pkg/spore-drive/proto/gen/config/v1/configv1connect"
	"go4.org/readerutil"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func copyFs(dstFs, srcFs afero.Fs) error {
	return afero.Walk(srcFs, "/", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			if err := dstFs.MkdirAll(path, info.Mode()); err != nil {
				return err
			}
		} else {
			srcFile, err := srcFs.Open(path)
			if err != nil {
				return err
			}
			defer srcFile.Close()

			var buf bytes.Buffer
			if _, err = io.Copy(&buf, srcFile); err != nil {
				return err
			}

			dstFile, err := dstFs.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
			if err != nil {
				return err
			}
			defer dstFile.Close()

			if _, err = io.Copy(dstFile, &buf); err != nil {
				return err
			}
		}

		return nil
	})
}

func filesystemFromBundle(bundle []byte, base string) (afero.Fs, error) {
	contentType, err := filetype.Match(bundle)
	if err != nil {
		return nil, fmt.Errorf("failed to determine bundle's type: %w", err)
	}

	var bundleFs afero.Fs
	switch contentType {
	case matchers.TypeZip:
		zipReader, err := zip.NewReader(
			readerutil.NewBufferingReaderAt(bytes.NewBuffer(bundle)),
			int64(len(bundle)),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to read zip bundle: %w", err)
		}

		bundleFs = zipfs.New(zipReader)

	case matchers.TypeTar:
		bundleFs = tarfs.New(tar.NewReader(bytes.NewBuffer(bundle)))
	default:
		return nil, errors.New("bundle format unsupported")
	}

	rfs := afero.NewMemMapFs()

	return rfs, copyFs(rfs, afero.NewBasePathFs(bundleFs, base))
}

func (s *Service) Upload(ctx context.Context, stream *connect.ClientStream[pb.SourceUpload]) (*connect.Response[pb.Config], error) {
	var (
		bundle []byte
		p      string
	)

	for stream.Receive() {
		select {
		case <-ctx.Done():
			return nil, connect.NewError(connect.CodeCanceled, errors.New("upload canceled"))
		default:
			req := stream.Msg()

			if x := req.GetPath(); x != "" {
				p = x
			} else if x := req.GetChunk(); x != nil {
				bundle = append(bundle, x...)
			} else {
				return nil, connect.NewError(connect.CodeUnknown, errors.New("unexpected payload"))
			}
		}
	}

	if err := stream.Err(); err != nil {
		return nil, connect.NewError(connect.CodeUnknown, err)
	}

	fs, err := filesystemFromBundle(bundle, p)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("mouting filesystem failed with %w", err))
	}

	c, err := s.newConfig(fs, "")
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("loading configuration failed with %s", err))
	}

	return connect.NewResponse(&pb.Config{Id: c.id}), nil
}

func (s *Service) New(context.Context, *connect.Request[pb.Empty]) (*connect.Response[pb.Config], error) {
	cnf, err := s.newConfig(afero.NewMemMapFs(), "")
	if err != nil {
		return nil, fmt.Errorf("failed to create config: %w", err)
	}

	return connect.NewResponse(&pb.Config{Id: cnf.id}), nil
}

func (s *Service) Load(ctx context.Context, req *connect.Request[pb.Source]) (*connect.Response[pb.Config], error) {
	root := req.Msg.GetRoot()
	if root == "" {
		return nil, errors.New("must provide root")
	}

	base := path.Clean(req.Msg.GetPath())

	if !path.IsAbs(base) {
		return nil, errors.New("path must be absolute")
	}

	st, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("failed to open root `%s`: %w", root, err)
	}

	var (
		fs       afero.Fs
		location string
	)

	if !st.IsDir() {
		bundle, err := os.ReadFile(root)
		if err != nil {
			return nil, fmt.Errorf("failed to open local root bundle `%s`: %w", root, err)
		}

		fs, err = filesystemFromBundle(bundle, base)
		if err != nil {
			return nil, fmt.Errorf("mouting filesystem failed with %w", err)
		}
	} else {
		location = path.Join(root, base)

		st, err = os.Stat(location)
		if err != nil {
			return nil, fmt.Errorf("failed to open `%s`: %w", location, err)
		}

		if !st.IsDir() {
			return nil, fmt.Errorf("%s must be a folder", location)
		}

		fs = afero.NewBasePathFs(afero.NewOsFs(), location)

	}

	cnf, err := s.newConfig(fs, location)
	if err != nil {
		return nil, fmt.Errorf("failed to create config: %w", err)
	}

	return connect.NewResponse(&pb.Config{Id: cnf.id}), nil
}

func (s *Service) Download(ctx context.Context, req *connect.Request[pb.BundleConfig], stream *connect.ServerStream[pb.Bundle]) (rerr error) {
	var cnf *configInstance
	if id := req.Msg.GetId(); id == nil {
		return errors.New("must provide config id")
	} else {
		cnf = s.getConfig(id.GetId())
		if cnf == nil {
			return errors.New("config not found")
		}
	}

	bundleType := req.Msg.GetType()

	err := stream.Send(&pb.Bundle{Data: &pb.Bundle_Type{Type: bundleType}})
	if err != nil {
		return status.Error(codes.Aborted, "failed to communicate type")
	}

	var wg sync.WaitGroup

	r, w := io.Pipe()
	defer func() {
		w.Close()
		wg.Wait()
	}()

	dctx, dctxC := context.WithCancel(ctx)

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer dctxC()

		buf := make([]byte, 1024*32) // 32 KB buffer size
		for {
			select {
			case <-dctx.Done():
				return
			default:
				n, err := r.Read(buf)
				if err != nil {
					if err == io.EOF {
						err = stream.Send(&pb.Bundle{Data: &pb.Bundle_Chunk{Chunk: buf[:n]}})
						if err != nil {
							rerr = fmt.Errorf("failed to send data with %w", err)
						}
						return
					}

					rerr = fmt.Errorf("failed to read data with %w", err)
					return
				}

				if n > 0 {
					err = stream.Send(&pb.Bundle{Data: &pb.Bundle_Chunk{Chunk: buf[:n]}})
					if err != nil {
						rerr = fmt.Errorf("failed to send data with %w", err)
						return
					}
				}
			}
		}

	}()

	switch bundleType {
	case pb.BundleType_BUNDLE_TAR:
		if err := tarFilesystem(dctx, cnf.fs, w); err != nil {
			dctxC()
			return fmt.Errorf("failed to generate tar with %w", err)
		}
	case pb.BundleType_BUNDLE_ZIP:
		if err := zipFilesystem(dctx, cnf.fs, w); err != nil {
			dctxC()
			return fmt.Errorf("failed to generate zip with %w", err)
		}
	default:
		dctxC()
		return status.Error(codes.Unknown, "unknown type")
	}

	return
}

func (s *Service) Free(ctx context.Context, in *connect.Request[pb.Config]) (*connect.Response[pb.Empty], error) {
	s.freeConfig(in.Msg.GetId())
	return noValReturn(nil)
}

func (s *Service) Commit(ctx context.Context, req *connect.Request[pb.Config]) (*connect.Response[pb.Empty], error) {
	var cnf *configInstance
	if c := req.Msg.GetId(); c == "" {
		return nil, errors.New("you must provide a configuration id")
	} else {
		cnf = s.getConfig(c)
		if cnf == nil {
			return nil, errors.New("configuration instance not found")
		}
	}

	cnf.lock.Lock()
	defer cnf.lock.Unlock()

	return noValReturn(cnf.parser.Sync())
}

func (s *Service) Do(ctx context.Context, req *connect.Request[pb.Op]) (*connect.Response[pb.Return], error) {
	var cnf *configInstance
	if c := req.Msg.GetConfig(); c == nil {
		return nil, errors.New("you must provide a configuration id")
	} else {
		cnf = s.getConfig(c.GetId())
		if cnf == nil {
			return nil, errors.New("configuration instance not found")
		}
	}

	cnf.lock.Lock()
	defer cnf.lock.Unlock()

	p := cnf.parser
	defer p.Sync()

	if q := req.Msg.GetCloud(); q != nil {
		return s.doCloud(q, p)
	}

	if q := req.Msg.GetHosts(); q != nil {
		return s.doHosts(q, p)
	}

	if q := req.Msg.GetAuth(); q != nil {
		return s.doAuth(q, p)
	}

	if q := req.Msg.GetShapes(); q != nil {
		return s.doShapes(q, p)
	}

	return connect.NewResponse(&pb.Return{}), nil
}

func (s *Service) Attach(mux *http.ServeMux) {
	mux.Handle(s.path, s.handler)
}

func Serve() (*Service, error) {
	s := &Service{
		configs: make(map[string]*configInstance),
	}

	s.path, s.handler = pbconnect.NewConfigServiceHandler(s)

	return s, nil
}
