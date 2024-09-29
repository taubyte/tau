package drive

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/taubyte/tau/pkg/mycelium/host/mocks"
	"github.com/taubyte/tau/pkg/spore-drive/course"
)

func TestProgress_Path(t *testing.T) {
	hypha := &course.Hypha{Name: "hypha-name"}
	host := new(mocks.Host)
	host.On("String").Return("host-name")
	stepName := "stepId"

	p := &progress{
		hypha:    hypha,
		host:     host,
		stepName: stepName,
	}

	expectedPath := "/hypha-name/host-name/stepId"
	assert.Equal(t, expectedPath, p.Path(), "Path() did not return the expected value")
}

func TestProgress_Name(t *testing.T) {
	p := &progress{
		stepName: "testStep",
	}

	assert.Equal(t, "testStep", p.Name(), "Name() did not return the expected value")
}

func TestProgress_Progress(t *testing.T) {
	p := &progress{
		progress: 42,
	}

	assert.Equal(t, 42, p.Progress(), "Progress() did not return the expected value")
}

func TestProgress_Error(t *testing.T) {
	p := &progress{
		err: errors.New("test error"),
	}

	assert.EqualError(t, p.Error(), "test error", "Error() did not return the expected error")

	// Test with no error
	p = &progress{}
	assert.Nil(t, p.Error(), "Error() should return nil when no error is set")
}
