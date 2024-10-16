package service

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/taubyte/tau/pkg/spore-drive/config"
	"github.com/taubyte/tau/pkg/spore-drive/config/fixtures"
	"github.com/taubyte/tau/pkg/spore-drive/drive"
	configPb "github.com/taubyte/tau/pkg/spore-drive/proto/gen/config/v1"
	pb "github.com/taubyte/tau/pkg/spore-drive/proto/gen/drive/v1"
	"gotest.tools/v3/assert"
)

// MockConfigResolver is a simple implementation of the ConfigResolver interface.
type MockConfigResolver struct {
	LookupFunc func(id string) (config.Parser, error)
}

func (m *MockConfigResolver) Lookup(id string) (config.Parser, error) {
	if m.LookupFunc != nil {
		return m.LookupFunc(id)
	}
	return nil, errors.New("not implemented")
}

// Helper function to initialize the service and mock dependencies.
func setupService() (*Service, *MockConfigResolver) {
	mockResolver := &MockConfigResolver{}
	svc := &Service{
		ctx:      context.TODO(),
		drives:   make(map[string]*driveInstance),
		courses:  make(map[string]*courseInstance),
		resolver: mockResolver,
	}
	return svc, mockResolver
}

func TestService_New(t *testing.T) {
	svc, mockResolver := setupService()

	// Use VirtConfig for a valid configuration
	_, cnf := fixtures.VirtConfig()
	mockResolver.LookupFunc = func(id string) (config.Parser, error) {
		if id == "valid_config_id" {
			return cnf, nil
		}
		return nil, errors.New("config not found")
	}

	req := &pb.DriveRequest{
		Config: &configPb.Config{Id: "valid_config_id"},
	}

	resp, err := svc.New(context.TODO(), connect.NewRequest(req))
	assert.NilError(t, err)
	assert.Equal(t, resp != nil, true)
	assert.Equal(t, resp.Msg != nil, true)
	assert.Equal(t, resp.Msg.GetId() != "", true)
	assert.Equal(t, svc.getDrive(resp.Msg.GetId()) != nil, true)
}

func TestService_New_MissingConfigID(t *testing.T) {
	svc, _ := setupService()

	req := &pb.DriveRequest{
		Config: &configPb.Config{}, // Missing ID
	}
	connectReq := connect.NewRequest(req)

	_, err := svc.New(context.TODO(), connectReq)
	assert.Error(t, err, "you need to provide config id")
}

func TestService_Free(t *testing.T) {
	svc, mockResolver := setupService()

	// Use VirtConfig for a valid configuration
	_, cnf := fixtures.VirtConfig()
	mockResolver.LookupFunc = func(id string) (config.Parser, error) {
		if id == "valid_config_id" {
			return cnf, nil
		}
		return nil, errors.New("config not found")
	}

	// First, create a drive using the New method
	req := &pb.DriveRequest{
		Config: &configPb.Config{Id: "valid_config_id"},
	}
	connectReq := connect.NewRequest(req)

	resp, err := svc.New(context.TODO(), connectReq)
	assert.NilError(t, err)
	assert.Equal(t, resp != nil, true)
	assert.Equal(t, resp.Msg != nil, true)

	driveID := resp.Msg.GetId()
	assert.Equal(t, driveID != "", true)
	assert.Equal(t, svc.getDrive(driveID) != nil, true)

	// Now free the drive
	freeReq := &pb.Drive{Id: driveID}
	freeConnectReq := connect.NewRequest(freeReq)

	freeResp, err := svc.Free(context.TODO(), freeConnectReq)
	assert.NilError(t, err)
	assert.Equal(t, freeResp != nil, true)

	// Ensure the drive is no longer stored
	assert.Equal(t, svc.getDrive(driveID) == nil, true)
}

func TestService_Plot(t *testing.T) {
	svc, mockResolver := setupService()

	// Use VirtConfig for a valid configuration
	_, cnf := fixtures.VirtConfig()
	mockResolver.LookupFunc = func(id string) (config.Parser, error) {
		if id == "valid_config_id" {
			return cnf, nil
		}
		return nil, errors.New("config not found")
	}

	// Create a drive
	req := &pb.DriveRequest{
		Config: &configPb.Config{Id: "valid_config_id"},
	}
	connectReq := connect.NewRequest(req)

	driveResp, err := svc.New(context.TODO(), connectReq)
	assert.NilError(t, err)
	assert.Equal(t, driveResp != nil, true)

	driveID := driveResp.Msg.GetId()
	assert.Equal(t, driveID != "", true)

	// Use the Plot method
	plotReq := &pb.PlotRequest{
		Drive:       &pb.Drive{Id: driveID},
		Concurrency: 2,
		Shapes:      []string{"shape1", "shape2"},
	}
	plotConnectReq := connect.NewRequest(plotReq)

	plotResp, err := svc.Plot(context.TODO(), plotConnectReq)
	assert.NilError(t, err)
	assert.Equal(t, plotResp != nil, true)
	assert.Equal(t, plotResp.Msg.GetId() != "", true)
}

func TestService_Abort(t *testing.T) {
	svc, mockResolver := setupService()

	// Use VirtConfig for a valid configuration
	_, cnf := fixtures.VirtConfig()
	mockResolver.LookupFunc = func(id string) (config.Parser, error) {
		if id == "valid_config_id" {
			return cnf, nil
		}
		return nil, errors.New("config not found")
	}

	// Create a drive
	req := &pb.DriveRequest{
		Config: &configPb.Config{Id: "valid_config_id"},
	}
	connectReq := connect.NewRequest(req)

	driveResp, err := svc.New(context.TODO(), connectReq)
	assert.NilError(t, err)
	assert.Equal(t, driveResp != nil, true)

	driveID := driveResp.Msg.GetId()
	assert.Equal(t, driveID != "", true)

	// Use the Plot method to create a course
	plotReq := &pb.PlotRequest{
		Drive:       &pb.Drive{Id: driveID},
		Concurrency: 2,
		Shapes:      []string{"shape1"},
	}
	plotConnectReq := connect.NewRequest(plotReq)

	plotResp, err := svc.Plot(context.TODO(), plotConnectReq)
	assert.NilError(t, err)
	assert.Equal(t, plotResp != nil, true)

	courseID := plotResp.Msg.GetId()
	assert.Equal(t, courseID != "", true)

	// Abort the course
	abortReq := &pb.Course{Id: courseID}
	abortConnectReq := connect.NewRequest(abortReq)

	abortResp, err := svc.Abort(context.TODO(), abortConnectReq)
	assert.NilError(t, err)
	assert.Equal(t, abortResp != nil, true)

	// Ensure the course is no longer stored
	assert.Equal(t, svc.getCourse(courseID) == nil, true)
}

func TestService_Displace(t *testing.T) {
	svc, mockResolver := setupService()

	// Use VirtConfig for a valid configuration
	_, cnf := fixtures.VirtConfig()
	mockResolver.LookupFunc = func(id string) (config.Parser, error) {
		if id == "valid_config_id" {
			return cnf, nil
		}
		return nil, errors.New("config not found")
	}

	// Create a drive
	req := &pb.DriveRequest{
		Config: &configPb.Config{Id: "valid_config_id"},
	}
	connectReq := connect.NewRequest(req)

	driveResp, err := svc.New(context.TODO(), connectReq)
	assert.NilError(t, err)
	assert.Assert(t, driveResp != nil)

	driveID := driveResp.Msg.GetId()
	assert.Assert(t, driveID != "")

	// Create a course using Plot
	plotReq := &pb.PlotRequest{
		Drive:       &pb.Drive{Id: driveID},
		Concurrency: 2,
		Shapes:      []string{"shape1", "shape2"},
	}
	plotConnectReq := connect.NewRequest(plotReq)

	plotResp, err := svc.Plot(context.TODO(), plotConnectReq)
	assert.NilError(t, err)
	assert.Assert(t, plotResp != nil)

	courseID := plotResp.Msg.GetId()
	assert.Assert(t, courseID != "")

	ci := svc.getCourse(courseID)
	pch := make(chan drive.Progress)
	defer close(pch)
	ci.mockDisplace = func() <-chan drive.Progress {
		return pch
	}

	// Call Displace
	displaceReq := &pb.Course{Id: courseID}
	displaceConnectReq := connect.NewRequest(displaceReq)

	displaceResp, err := svc.Displace(context.TODO(), displaceConnectReq)
	assert.NilError(t, err)
	assert.Assert(t, displaceResp != nil)
}
