package singletonsI18n

import (
	"errors"
	"fmt"
)

const (
	// Common
	creatingSeerAtLocFailed = "creating seer at `%s` failed with: %w"

	// Session
	sessionFileLocationEmpty  = "session file location is empty and could not discover or create"
	sessionSettingKeyFailed   = "setting session key `%s` to `%s` failed with: %w"
	sessionDeletingKeyFailed  = "deleting session key `%s`  failed with: %w"
	sessionDeleteFailed       = "deleting session at %s failed with: %w"
	sessionListFailed         = "getting session items failed with: %w"
	sessionNotFound           = "no session found"
	sessionCreateFailed       = "creating session file at `%s` failed with: %w"
	creatingSessionFileFailed = "creating session file failed with: %w"

	// Config
	creatingConfigFileFailed = "creating config file failed with: %w"
	gettingProfileFailedWith = "getting profile `%s` from config failed with: %w"
	settingProfileFailedWith = "setting profile `%s` in config failed with: %w"

	gettingProjectFailedWith   = "getting project `%s` from config failed with: %w"
	settingProjectFailedWith   = "setting project `%s` in config failed with: %w"
	deletingProjectFailedWith  = "deleting project `%s` from config failed with: %w"
	projectLocationNotFound    = "project `%s` location not found"
	openingProjectConfigFailed = "opening project config at `%s` failed with: %w"
	projectAlreadyCloned       = "project `%s` already cloned in: `%s`"

	// Auth_client
	profileDoesNotExist      = "profile does not exist"
	creatingAuthClientFailed = "creating auth client failed with: %w"
	loadingClientFailed      = "loading %s client failed with: %w"

	//Network
	noNetworkSelected = "no network selected"
)

func CreatingSeerAtLocFailed(loc string, err error) error {
	return fmt.Errorf(creatingSeerAtLocFailed, loc, err)
}

func SessionFileLocationEmpty() error {
	return errors.New(sessionFileLocationEmpty)
}

func SessionSettingKeyFailed(key string, value interface{}, err error) error {
	return fmt.Errorf(sessionSettingKeyFailed, key, value, err)
}

func SessionDeletingKeyFailed(key string, err error) error {
	return fmt.Errorf(sessionDeletingKeyFailed, key, err)
}

func SessionDeleteFailed(loc string, err error) error {
	return fmt.Errorf(sessionDeleteFailed, loc, err)
}

func SessionListFailed(err error) error {
	return fmt.Errorf(sessionListFailed, err)
}

func SessionNotFound() error {
	return errors.New(sessionNotFound)
}

func SessionCreateFailed(loc string, err error) error {
	return fmt.Errorf(sessionCreateFailed, loc, err)
}

func CreatingSessionFileFailed(err error) error {
	return fmt.Errorf(creatingSessionFileFailed, err)
}

func CreatingConfigFileFailed(err error) error {
	return fmt.Errorf(creatingConfigFileFailed, err)
}

func GettingProfileFailedWith(profile string, err error) error {
	return fmt.Errorf(gettingProfileFailedWith, profile, err)
}

func SettingProfileFailedWith(profile string, err error) error {
	return fmt.Errorf(settingProfileFailedWith, profile, err)
}

func GettingProjectFailedWith(project string, err error) error {
	return fmt.Errorf(gettingProjectFailedWith, project, err)
}

func SettingProjectFailedWith(project string, err error) error {
	return fmt.Errorf(settingProjectFailedWith, project, err)
}

func DeletingProjectFailedWith(project string, err error) error {
	return fmt.Errorf(deletingProjectFailedWith, project, err)
}

func ProjectLocationNotFound(project string) error {
	return fmt.Errorf(projectLocationNotFound, project)
}

func OpeningProjectConfigFailed(loc string, err error) error {
	return fmt.Errorf(openingProjectConfigFailed, loc, err)
}

func ProjectAlreadyCloned(project, loc string) error {
	return fmt.Errorf(projectAlreadyCloned, project, loc)
}

func ProfileDoesNotExist() error {
	return errors.New(profileDoesNotExist)
}

func CreatingAuthClientFailed(err error) error {
	return fmt.Errorf(creatingAuthClientFailed, err)
}

func LoadingAuthClientFailed(err error) error {
	return fmt.Errorf(loadingClientFailed, "auth", err)
}

func CreatingPatrickClientFailed(err error) error {
	return fmt.Errorf(creatingAuthClientFailed, err)
}

func LoadingPatrickClientFailed(err error) error {
	return fmt.Errorf(loadingClientFailed, "patrick", err)
}

func NoNetworkSelected() error {
	return errors.New(noNetworkSelected)
}
