package fixtures

import (
	"fmt"
	"testing"

	projectSchema "github.com/taubyte/tau/pkg/schema/project"
)

func TestFixture(t *testing.T) {
	fs, err := VirtualFSWithBuiltProject()
	if err != nil {
		t.Errorf("VirtualFS failed with: %v", err)
		return
	}

	project, err := projectSchema.Open(projectSchema.VirtualFS(fs, "/test_project/config"))
	if err != nil {
		t.Error(err)
		return
	}
	getter := project.Get()
	appName := testAppName
	dL, dG := getter.Databases(appName)
	fmt.Println("Databases", dL, dG)

	doL, doG := getter.Domains(appName)
	fmt.Println("Domains", doL, doG)

	fL, fG := getter.Functions(appName)
	fmt.Println("Functions", fL, fG)

	lL, lG := getter.Libraries(appName)
	fmt.Println("Libraries", lL, lG)

	mL, mG := getter.Messaging(appName)
	fmt.Println("Messaging", mL, mG)

	soL, soG := getter.SmartOps(appName)
	fmt.Println("SmartOps", soL, soG)

	stL, stG := getter.Storages(appName)
	fmt.Println("Storages", stL, stG)

	wL, wG := getter.Websites(appName)
	fmt.Println("Websites", wL, wG)

}
