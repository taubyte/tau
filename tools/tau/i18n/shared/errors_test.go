package singletonsI18n_test

import (
	"errors"
	"testing"

	singletonsI18n "github.com/taubyte/tau/tools/tau/i18n/shared"
	"gotest.tools/v3/assert"
)

func TestSessionFileLocationEmpty(t *testing.T) {
	err := singletonsI18n.SessionFileLocationEmpty()
	assert.ErrorContains(t, err, "session file location")
}

func TestSessionNotFound(t *testing.T) {
	err := singletonsI18n.SessionNotFound()
	assert.ErrorContains(t, err, "no session found")
}

func TestSessionListFailed(t *testing.T) {
	err := singletonsI18n.SessionListFailed(errors.New("err"))
	assert.ErrorContains(t, err, "session")
}

func TestNoCloudSelected(t *testing.T) {
	err := singletonsI18n.NoCloudSelected()
	assert.ErrorContains(t, err, "no cloud selected")
}

func TestProfileDoesNotExist(t *testing.T) {
	err := singletonsI18n.ProfileDoesNotExist()
	assert.ErrorContains(t, err, "profile does not exist")
}

func TestCreatingSeerAtLocFailed(t *testing.T) {
	err := singletonsI18n.CreatingSeerAtLocFailed("/path", errors.New("err"))
	assert.ErrorContains(t, err, "/path")
}

func TestProjectLocationNotFound(t *testing.T) {
	err := singletonsI18n.ProjectLocationNotFound("proj")
	assert.ErrorContains(t, err, "proj")
}

func TestProjectAlreadyCloned(t *testing.T) {
	err := singletonsI18n.ProjectAlreadyCloned("proj", "/loc")
	assert.ErrorContains(t, err, "proj")
	assert.ErrorContains(t, err, "/loc")
}

func TestSessionSettingKeyFailed(t *testing.T) {
	err := singletonsI18n.SessionSettingKeyFailed("key", "val", errors.New("e"))
	assert.ErrorContains(t, err, "key")
	assert.ErrorContains(t, err, "e")
}

func TestSessionDeletingKeyFailed(t *testing.T) {
	err := singletonsI18n.SessionDeletingKeyFailed("key", errors.New("e"))
	assert.ErrorContains(t, err, "key")
}

func TestSessionDeleteFailed(t *testing.T) {
	err := singletonsI18n.SessionDeleteFailed("/loc", errors.New("e"))
	assert.ErrorContains(t, err, "/loc")
}

func TestSessionCreateFailed(t *testing.T) {
	err := singletonsI18n.SessionCreateFailed("/loc", errors.New("e"))
	assert.ErrorContains(t, err, "/loc")
}

func TestCreatingSessionFileFailed(t *testing.T) {
	err := singletonsI18n.CreatingSessionFileFailed(errors.New("e"))
	assert.ErrorContains(t, err, "session file")
}

func TestCreatingConfigFileFailed(t *testing.T) {
	err := singletonsI18n.CreatingConfigFileFailed(errors.New("e"))
	assert.ErrorContains(t, err, "config file")
}

func TestGettingProfileFailedWith(t *testing.T) {
	err := singletonsI18n.GettingProfileFailedWith("p1", errors.New("e"))
	assert.ErrorContains(t, err, "p1")
}

func TestSettingProfileFailedWith(t *testing.T) {
	err := singletonsI18n.SettingProfileFailedWith("p1", errors.New("e"))
	assert.ErrorContains(t, err, "p1")
}

func TestGettingProjectFailedWith(t *testing.T) {
	err := singletonsI18n.GettingProjectFailedWith("proj", errors.New("e"))
	assert.ErrorContains(t, err, "proj")
}

func TestSettingProjectFailedWith(t *testing.T) {
	err := singletonsI18n.SettingProjectFailedWith("proj", errors.New("e"))
	assert.ErrorContains(t, err, "proj")
}

func TestDeletingProjectFailedWith(t *testing.T) {
	err := singletonsI18n.DeletingProjectFailedWith("proj", errors.New("e"))
	assert.ErrorContains(t, err, "proj")
}

func TestOpeningProjectConfigFailed(t *testing.T) {
	err := singletonsI18n.OpeningProjectConfigFailed("/path", errors.New("e"))
	assert.ErrorContains(t, err, "/path")
}

func TestCreatingAuthClientFailed(t *testing.T) {
	err := singletonsI18n.CreatingAuthClientFailed(errors.New("e"))
	assert.ErrorContains(t, err, "auth client")
}

func TestLoadingAuthClientFailed(t *testing.T) {
	err := singletonsI18n.LoadingAuthClientFailed(errors.New("e"))
	assert.ErrorContains(t, err, "auth")
}

func TestCreatingPatrickClientFailed(t *testing.T) {
	err := singletonsI18n.CreatingPatrickClientFailed(errors.New("e"))
	assert.ErrorContains(t, err, "creating auth client")
}

func TestLoadingPatrickClientFailed(t *testing.T) {
	err := singletonsI18n.LoadingPatrickClientFailed(errors.New("e"))
	assert.ErrorContains(t, err, "patrick")
}
