package service

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"

	"github.com/h2non/filetype"
	"github.com/h2non/filetype/matchers"
	"github.com/spf13/afero"
	"github.com/spf13/afero/tarfs"
	"github.com/spf13/afero/zipfs"
	pb "github.com/taubyte/tau/pkg/spore-drive/config/proto/go"
	"go4.org/readerutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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

	return afero.NewCopyOnWriteFs(afero.NewBasePathFs(bundleFs, base), afero.NewMemMapFs()), nil
}

func (s *Service) Upload(stream grpc.ClientStreamingServer[pb.SourceUpload, pb.Config]) error {
	var (
		bundle []byte
		p      string
	)

	for {
		req, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return status.Errorf(codes.Aborted, "upload failed with %s", err.Error())
		}

		if x := req.GetPath(); x != "" {
			p = x
		} else if x := req.GetChunk(); x != nil {
			bundle = append(bundle, x...)
		} else {
			return status.Errorf(codes.Aborted, "unexpected payload")
		}
	}

	fs, err := filesystemFromBundle(bundle, p)
	if err != nil {
		return status.Errorf(codes.Internal, "mouting filesystem failed with %s", err.Error())
	}

	c, err := s.newConfig(fs, "")
	if err != nil {
		return status.Errorf(codes.Internal, "loading configuration failed with %s", err.Error())
	}

	return stream.SendAndClose(&pb.Config{Id: c.id})
}

func (s *Service) Load(ctx context.Context, in *pb.Source) (*pb.Config, error) {

	root := in.GetRoot()
	if root == "" {
		return nil, errors.New("must provide root")
	}

	base := path.Clean(in.GetPath())

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

		fs = afero.NewCopyOnWriteFs(afero.NewBasePathFs(afero.NewOsFs(), location), afero.NewMemMapFs())

	}

	cnf, err := s.newConfig(fs, location)
	if err != nil {
		return nil, fmt.Errorf("failed to create config: %w", err)
	}

	return &pb.Config{Id: cnf.id}, nil
}

func (s *Service) Download(in *pb.BundleConfig, stream grpc.ServerStreamingServer[pb.Bundle]) error {
	var cnf *configInstance
	if id := in.GetId(); id == nil {
		return errors.New("must provide config id")
	} else {
		cnf = s.getConfig(id.GetId())
		if cnf == nil {
			return errors.New("config not found")
		}
	}

	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()

	go func() {
		buf := make([]byte, 1024*32) // 32 KB buffer size
		for {
			n, err := r.Read(buf)
			if err != nil {
				if err == io.EOF {
					break
				}
				w.CloseWithError(err)
				return
			}

			err = stream.Send(&pb.Bundle{Data: &pb.Bundle_Chunk{Chunk: buf[:n]}})
			if err != nil {
				w.CloseWithError(err)
				return
			}
		}
	}()

	bundleType := in.GetType()

	err := stream.Send(&pb.Bundle{Data: &pb.Bundle_Type{Type: bundleType}})
	if err != nil {
		return status.Error(codes.Aborted, "failed to communicate type")
	}

	switch bundleType {
	case pb.BundleType_BUNDLE_TAR:
		return tarFilesystem(cnf.fs, w)
	case pb.BundleType_BUNDLE_ZIP:
		return zipFilesystem(cnf.fs, w)
	default:
		return status.Error(codes.Unknown, "unknown type")
	}
}

func (s *Service) Free(ctx context.Context, in *pb.Config) (*pb.Empty, error) {
	s.freeConfig(in.GetId())
	return &pb.Empty{}, nil
}

func (s *Service) Commit(context.Context, *pb.BundleConfig) (*pb.Empty, error) {

	return nil, nil
}

func (s *Service) Do(ctx context.Context, in *pb.Op) (*pb.Return, error) {
	var cnf *configInstance //config.Parser
	if c := in.GetConfig(); c == nil {
		return nil, errors.New("you must provide a configuration id")
	} else {
		// check if config exists
		cnf = s.getConfig(c.GetId())
		if cnf == nil {
			return nil, errors.New("configuration instance not found")
		}

	}

	cnf.lock.Lock()
	defer cnf.lock.Unlock()

	p := cnf.parser
	defer p.Sync()

	if q := in.GetCloud(); q != nil {
		return s.doCloud(q, p)
	}

	if q := in.GetHosts(); q != nil {
		return s.doHosts(q, p)
	}

	if q := in.GetAuth(); q != nil {
		return s.doAuth(q, p)
	}

	if q := in.GetShapes(); q != nil {
		return s.doShapes(q, p)
	}

	return &pb.Return{}, nil
}

func Serve(server grpc.ServiceRegistrar) (*Service, error) {
	srv := &Service{}
	// Register the server.
	pb.RegisterConfigServiceServer(server, srv)

	return srv, nil
}
