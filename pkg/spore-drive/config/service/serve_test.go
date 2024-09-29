package service

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/taubyte/tau/pkg/spore-drive/config/fixtures"
	pb "github.com/taubyte/tau/pkg/spore-drive/config/proto/go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestFilesystemFromBundle_InvalidType(t *testing.T) {
	bundle := []byte("invalid data")
	_, err := filesystemFromBundle(bundle, "/")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "bundle format unsupported")
}

func TestFilesystemFromBundle_ValidZip(t *testing.T) {
	fs, _ := fixtures.VirtConfig()
	var buf bytes.Buffer
	err := zipFilesystem(fs, &buf)
	assert.NoError(t, err)
	bundleData := buf.Bytes()

	fsResult, err := filesystemFromBundle(bundleData, "/")
	assert.NoError(t, err)
	assert.NotNil(t, fsResult)
}

func TestFilesystemFromBundle_ValidTar(t *testing.T) {
	fs, _ := fixtures.VirtConfig()
	var buf bytes.Buffer
	err := tarFilesystem(fs, &buf)
	assert.NoError(t, err)
	bundleData := buf.Bytes()

	fsResult, err := filesystemFromBundle(bundleData, "/")
	assert.NoError(t, err)
	assert.NotNil(t, fsResult)
}

func TestUpload_ValidBundle(t *testing.T) {
	service := &Service{configs: make(map[string]*configInstance)}
	fs, _ := fixtures.VirtConfig()
	var buf bytes.Buffer
	err := zipFilesystem(fs, &buf)
	assert.NoError(t, err)
	bundleData := buf.Bytes()

	stream := &MockUploadStream{
		RecvFunc: func() (*pb.SourceUpload, error) {
			if len(bundleData) == 0 {
				return nil, io.EOF
			}
			chunkSize := 1024
			if len(bundleData) < chunkSize {
				chunkSize = len(bundleData)
			}
			chunk := bundleData[:chunkSize]
			bundleData = bundleData[chunkSize:]
			return &pb.SourceUpload{Data: &pb.SourceUpload_Chunk{Chunk: chunk}}, nil
		},
		SendAndCloseFunc: func(cfg *pb.Config) error {
			assert.NotEmpty(t, cfg.GetId())
			return nil
		},
	}

	err = service.Upload(stream)
	assert.NoError(t, err)
}

func TestUpload_InvalidPayload(t *testing.T) {
	service := &Service{configs: make(map[string]*configInstance)}
	stream := &MockUploadStream{
		RecvFunc: func() (*pb.SourceUpload, error) {
			return &pb.SourceUpload{}, nil
		},
		SendAndCloseFunc: func(cfg *pb.Config) error {
			return nil
		},
	}

	err := service.Upload(stream)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected payload")
}

func TestUpload_InvalidBundleData(t *testing.T) {
	service := &Service{configs: make(map[string]*configInstance)}
	invalidBundleData := []byte("invalid data")
	stream := &MockUploadStream{
		RecvFunc: func() (*pb.SourceUpload, error) {
			if len(invalidBundleData) == 0 {
				return nil, io.EOF
			}
			chunkSize := 1024
			if len(invalidBundleData) < chunkSize {
				chunkSize = len(invalidBundleData)
			}
			chunk := invalidBundleData[:chunkSize]
			invalidBundleData = invalidBundleData[chunkSize:]
			return &pb.SourceUpload{Data: &pb.SourceUpload_Chunk{Chunk: chunk}}, nil
		},
		SendAndCloseFunc: func(cfg *pb.Config) error {
			return nil
		},
	}

	err := service.Upload(stream)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "mouting filesystem failed")
}

func TestUpload_RecvError(t *testing.T) {
	service := &Service{configs: make(map[string]*configInstance)}
	stream := &MockUploadStream{
		RecvFunc: func() (*pb.SourceUpload, error) {
			return nil, io.ErrUnexpectedEOF
		},
		SendAndCloseFunc: func(cfg *pb.Config) error {
			return nil
		},
	}

	err := service.Upload(stream)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "upload failed with unexpected EOF")
}

func TestLoad_EmptyRoot(t *testing.T) {
	service := &Service{configs: make(map[string]*configInstance)}
	in := &pb.Source{
		Root: "",
		Path: "/config",
	}
	_, err := service.Load(context.Background(), in)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must provide root")
}

func TestLoad_RelativePath(t *testing.T) {
	service := &Service{configs: make(map[string]*configInstance)}
	in := &pb.Source{
		Root: "/some/root",
		Path: "relative/path",
	}
	_, err := service.Load(context.Background(), in)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "path must be absolute")
}

func TestLoad_InvalidRoot(t *testing.T) {
	service := &Service{configs: make(map[string]*configInstance)}
	in := &pb.Source{
		Root: "/invalid/root",
		Path: "/config",
	}
	_, err := service.Load(context.Background(), in)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to open root")
}

func TestLoad_ValidBundle(t *testing.T) {
	service := &Service{configs: make(map[string]*configInstance)}
	// Create a zip bundle of the virtual config
	fs, _ := fixtures.VirtConfig()
	var buf bytes.Buffer
	err := zipFilesystem(fs, &buf)
	assert.NoError(t, err)
	// Write bundle to a temporary file
	tempFile, err := os.CreateTemp("", "bundle.zip")
	assert.NoError(t, err)
	defer os.Remove(tempFile.Name())
	_, err = tempFile.Write(buf.Bytes())
	assert.NoError(t, err)
	tempFile.Close()
	in := &pb.Source{
		Root: tempFile.Name(),
		Path: "/",
	}
	cfg, err := service.Load(context.Background(), in)
	assert.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.NotEmpty(t, cfg.GetId())
}

func TestDownload_InvalidConfigID(t *testing.T) {
	service := &Service{configs: make(map[string]*configInstance)}
	in := &pb.BundleConfig{
		Id:   &pb.Config{Id: "invalid-id"},
		Type: pb.BundleType_BUNDLE_ZIP,
	}
	stream := &MockDownloadStream{}
	err := service.Download(in, stream)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "config not found")
}

func TestDownload_ValidConfig(t *testing.T) {
	service := &Service{configs: make(map[string]*configInstance)}
	fs, _ := fixtures.VirtConfig()
	configInst, err := service.newConfig(fs, "")
	assert.NoError(t, err)
	in := &pb.BundleConfig{
		Id:   &pb.Config{Id: configInst.id},
		Type: pb.BundleType_BUNDLE_ZIP,
	}
	stream := &MockDownloadStream{}
	err = service.Download(in, stream)
	assert.NoError(t, err)
	// Check that data was sent
	assert.NotEmpty(t, stream.SentMessages)
	// First message should be the type
	firstMessage := stream.SentMessages[0]
	assert.Equal(t, pb.BundleType_BUNDLE_ZIP, firstMessage.GetType())
	// Subsequent messages should be chunks
	for _, msg := range stream.SentMessages[1:] {
		assert.NotNil(t, msg.GetChunk())
	}
}

func TestDownload_UnknownBundleType(t *testing.T) {
	service := &Service{configs: make(map[string]*configInstance)}
	fs, _ := fixtures.VirtConfig()
	configInst, err := service.newConfig(fs, "")
	assert.NoError(t, err)
	in := &pb.BundleConfig{
		Id:   &pb.Config{Id: configInst.id},
		Type: pb.BundleType(999), // Unknown type
	}
	stream := &MockDownloadStream{}
	err = service.Download(in, stream)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown type")
}

func TestDownload_NoConfigID(t *testing.T) {
	service := &Service{configs: make(map[string]*configInstance)}
	in := &pb.BundleConfig{
		Type: pb.BundleType_BUNDLE_ZIP,
	}
	stream := &MockDownloadStream{}
	err := service.Download(in, stream)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must provide config id")
}

func TestDownload_SendError(t *testing.T) {
	service := &Service{configs: make(map[string]*configInstance)}
	fs, _ := fixtures.VirtConfig()
	configInst, err := service.newConfig(fs, "")
	assert.NoError(t, err)
	in := &pb.BundleConfig{
		Id:   &pb.Config{Id: configInst.id},
		Type: pb.BundleType_BUNDLE_ZIP,
	}
	stream := &MockDownloadStream{
		SendError: io.ErrUnexpectedEOF,
	}
	err = service.Download(in, stream)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to communicate type")
}

func TestFree(t *testing.T) {
	service := &Service{configs: make(map[string]*configInstance)}
	fs, _ := fixtures.VirtConfig()
	configInst, err := service.newConfig(fs, "")
	assert.NoError(t, err)
	assert.NotNil(t, configInst)
	cfg := &pb.Config{Id: configInst.id}
	_, err = service.Free(context.Background(), cfg)
	assert.NoError(t, err)
	// Ensure that the config is removed
	assert.Nil(t, service.getConfig(configInst.id))
}

func TestDo_NoConfig(t *testing.T) {
	service := &Service{configs: make(map[string]*configInstance)}
	in := &pb.Op{}
	_, err := service.Do(context.Background(), in)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "you must provide a configuration id")
}

func TestDo_ConfigNotFound(t *testing.T) {
	service := &Service{configs: make(map[string]*configInstance)}
	in := &pb.Op{
		Config: &pb.Config{Id: "invalid-id"},
	}
	_, err := service.Do(context.Background(), in)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "configuration instance not found")
}

func TestDo_ValidCloudOperation(t *testing.T) {
	service := &Service{configs: make(map[string]*configInstance)}
	fs, _ := fixtures.VirtConfig()
	configInst, err := service.newConfig(fs, "")
	assert.NoError(t, err)
	in := &pb.Op{
		Config: &pb.Config{Id: configInst.id},
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
	}
	resp, err := service.Do(context.Background(), in)
	assert.NoError(t, err)
	assert.Equal(t, "test.com", resp.GetString_())
}

func TestCommit(t *testing.T) {
	service := &Service{configs: make(map[string]*configInstance)}
	in := &pb.BundleConfig{}
	resp, err := service.Commit(context.Background(), in)
	assert.Nil(t, resp)
	assert.NoError(t, err)
}

type MockUploadStream struct {
	grpc.ServerStream
	RecvFunc         func() (*pb.SourceUpload, error)
	SendAndCloseFunc func(*pb.Config) error
}

func (m *MockUploadStream) Recv() (*pb.SourceUpload, error) {
	return m.RecvFunc()
}

func (m *MockUploadStream) SendAndClose(cfg *pb.Config) error {
	return m.SendAndCloseFunc(cfg)
}

func (m *MockUploadStream) Context() context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.MD{})
}

type MockDownloadStream struct {
	grpc.ServerStream
	SentMessages []*pb.Bundle
	SendError    error
}

func (m *MockDownloadStream) Send(bundle *pb.Bundle) error {
	if m.SendError != nil {
		return m.SendError
	}
	m.SentMessages = append(m.SentMessages, bundle)
	return nil
}

func (m *MockDownloadStream) Context() context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.MD{})
}
