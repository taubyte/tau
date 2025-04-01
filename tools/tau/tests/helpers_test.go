package tests

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	commonTest "github.com/taubyte/tau/tools/tau/common/test"
	"github.com/taubyte/tau/tools/tau/singletons/session"
	"github.com/taubyte/utils/x509"
)

func basicGetConfigString(profileName string, projectName string) func(dir string) []byte {
	return func(dir string) []byte {
		return []byte(`
profiles:
  ` + profileName + `:
    provider: github
    token: 123456
    default: true
    git_username: taubyte-test
    git_email: taubytetest@gmail.com
    type: Remote
    network: sandbox.taubyte.com
projects:
  ` + projectName + `:
    defaultprofile: ` + profileName + `
    location: ` + projectName)
	}
}

func basicValidConfigString(t *testing.T, profileName string, projectName string) func(dir string) []byte {
	return func(dir string) []byte {
		return []byte(`
profiles:
  ` + profileName + `:
    provider: github
    token: ` + commonTest.GitToken(t) + `
    default: true
    git_username: ` + commonTest.GitUser + `
    git_email: taubytetest@gmail.com
    type: Remote
    network: sandbox.taubyte.com
projects:
  ` + projectName + `:
    defaultprofile: ` + profileName + `
    location: ` + projectName)
	}
}

func certWriteFilesInDir(hostName string, pathArgs ...string) func(dir string) {
	var _path string
	if len(pathArgs) == 0 {
		_path = ""
	} else {
		// TODO make this magical
		_path = path.Join(pathArgs...)
	}
	return func(dir string) {
		if len(hostName) == 0 {
			return
		}
		certData, privateKey, err := x509.GenerateCert(hostName)
		if err != nil {
			panic(fmt.Sprintf("GenerateCert for host `%s` failed with: %s", hostName, err))
		}

		// Write cert file
		certFilePath, err := filepath.Abs(path.Join(dir, _path, "testcert.crt"))
		if err != nil {
			panic(fmt.Sprintf("Make path: %s failed with error: %s", path.Join(_path, "testcert.crt"), err.Error()))
		}
		err = os.WriteFile(certFilePath, certData, 0640)
		if err != nil {
			panic(fmt.Sprintf("Write file: %s failed with error: %s", certFilePath, err.Error()))
		}

		// Write key file
		keyFilePath, err := filepath.Abs(path.Join(dir, _path, "key.key"))
		if err != nil {
			panic(fmt.Sprintf("Make path: %s failed with error: %s", path.Join(_path, "key.key"), err.Error()))
		}

		err = os.WriteFile(keyFilePath, privateKey, 0640)
		if err != nil {
			panic(fmt.Sprintf("Write file: %s failed with error: %s", keyFilePath, err.Error()))
		}
	}
}

// TODO Move to utils
func stringContainsAll(query string, items []string) bool {
	for _, s := range items {
		if !strings.Contains(query, s) {
			return false
		}
	}
	return true
}

// TODO Move to utils
func stringContainsAny(query string, items []string) bool {
	for _, s := range items {
		if strings.Contains(query, s) {
			return true
		}
	}
	return false
}

// evaluateSession helper
func expectProfileName(expected string) func(g session.Getter) error {
	return func(g session.Getter) error {
		profileName, _ := g.ProfileName()
		if profileName != expected {
			return fmt.Errorf("expected profile name to be `%s`, got `%s`", expected, profileName)
		}

		return nil
	}
}

// evaluateSession helper
func expectSelectedProject(expected string) func(g session.Getter) error {
	return func(g session.Getter) error {
		selectedProject, _ := g.SelectedProject()
		if selectedProject != expected {
			return fmt.Errorf("expected project name to be `%s`, got `%s`", expected, selectedProject)
		}

		return nil
	}
}

// evaluateSession helper
func expectedSelectedNetwork(expected string) func(g session.Getter) error {
	return func(g session.Getter) error {
		network, _ := g.SelectedNetwork()
		if network != expected {
			return fmt.Errorf("Network does not match, %s != %s", expected, network)
		}

		return nil
	}
}

func isEmpty(val interface{}) bool {
	switch v := val.(type) {
	case string:
		return v == ""
	case int, int16, int32, int64, int8:
		return v == 0
	case uint, uint16, uint32, uint64, uint8:
		return v == 0
	case float32, float64:
		return v == 0.0
	case bool:
		return !v
	default:
		reflectVal := reflect.ValueOf(val)
		switch reflectVal.Kind() {
		case reflect.Array, reflect.Map, reflect.Slice:
			return reflectVal.Len() == 0
		case reflect.Chan, reflect.Func, reflect.Interface, reflect.Ptr:
			return reflectVal.IsNil()
		case reflect.Struct:
			return reflectVal.IsZero()
		default:
			return false
		}
	}
}

func ConfirmEmpty(values ...any) error {
	for _, val := range values {
		if !isEmpty(val) {
			return fmt.Errorf("%T (%#v) is not empty", val, val)
		}
	}

	return nil
}
