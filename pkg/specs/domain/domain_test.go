package domainSpec

import "testing"

var (
	domainName     = "taubyte"
	topLevelDomain = "com"
	rootDomain     = domainName + "." + topLevelDomain
)

func TestDomain(t *testing.T) {
	_path, err := Tns().BasicPath(rootDomain)
	if err != nil {
		t.Error(err)
		return
	}

	expectedPath := PathVariable.String() + "/" + topLevelDomain + "/" + domainName
	if _path.String() != expectedPath {
		t.Errorf("Got `%s` expected `%s`", _path, expectedPath)
		return
	}

}
