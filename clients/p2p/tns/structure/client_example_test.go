package structure_test

import (
	"fmt"
	"sort"

	_ "github.com/taubyte/config-compiler/fixtures"
	_ "github.com/taubyte/tau/clients/p2p/tns"
	dreamland "github.com/taubyte/tau/libdream/services"
	_ "github.com/taubyte/tau/protocols/tns"
)

func ExampleClient() {
	u, tns, err := dreamland.BasicMultiverse("ExampleClient").Tns()
	if err != nil {
		fmt.Println("BasicMultiverse failed with", err)
		return
	}
	defer u.Stop()

	err = u.RunFixture("fakeProject")
	if err != nil {
		fmt.Println("Fixture failed with", err)
		return
	}

	all, err := tns.Storage().All(testProjectId, testAppId, testBranch).List()
	if err != nil {
		fmt.Println("List failed with", err)
		return
	}

	ids := []string{}

	for id := range all {
		ids = append(ids, id)
	}

	newIds := sort.StringSlice(ids)
	newIds.Sort()
	fmt.Printf("All storage Ids: %v\n", newIds)

	// Output: All storage Ids: [QmV2KtAPhZHjFhH4iWXZFkWzB92iFUVHWScLNU5YEGLOBAL QmV2KtAPhZHjFhH4iWXZFkWzB92iFUVHWScLNU5YELOCAL QmVaeAmXrE4Zy94BYp3CG5UKDhmvB4gTdk72pG1oyKVbAe]
}
