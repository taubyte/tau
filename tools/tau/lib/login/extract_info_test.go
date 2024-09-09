package loginLib

import (
	"testing"

	commonTest "github.com/taubyte/tau/tools/tau/common/test"
)

func TestInfo(t *testing.T) {
	t.Skip("Github end point returns 404")
	name, email, err := extractInfo(commonTest.GitToken(t), "github")
	if err != nil {
		t.Error(err)
		return
	}

	expectedName := "taubyte-test"
	expectedEmail := "taubytetest@gmail.com"

	if name != expectedName {
		t.Errorf("Expected name: %s, got: %s", expectedName, name)
	}
	if email != expectedEmail {
		t.Errorf("Expected email: %s, got: %s", expectedEmail, email)
	}
}
