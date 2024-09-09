package projectTable_test

import (
	client "github.com/taubyte/tau/clients/http/auth"
	projectTable "github.com/taubyte/tau/tools/tau/table/project"
)

func ExampleQuery() {
	project := &client.Project{
		Id:   "QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH",
		Name: "someProject",
	}

	repoData := &client.RawRepoDataOuter{
		Configuration: client.RawRepoData{
			Fullname: "taubyte-test/tb_test_project",
			Url:      "https://api.github.com/repos/taubyte-test/tb_test_project",
		},
		Code: client.RawRepoData{
			Fullname: "taubyte-test/tb_code_test_project",
			Url:      "https://api.github.com/repos/taubyte-test/tb_code_test_project",
		},
		Provider: "github",
	}

	projectTable.Query(project, repoData, "some")

	// Output:
	// ┌─────────────┬──────────────────────────────────────────────────────┐
	// │ ID          │ QmbAA8hRosp5BaXFXikADCtpkQCgQCPdRVhnxjiSHfXdWH       │
	// ├─────────────┼──────────────────────────────────────────────────────┤
	// │ Name        │ someProject                                          │
	// ├─────────────┼──────────────────────────────────────────────────────┤
	// │ Description │ some                                                 │
	// ├─────────────┼──────────────────────────────────────────────────────┤
	// │             │ Code                                                 │
	// │ Name:       │ taubyte-test/tb_code_test_project                    │
	// │ URL:        │ https://github.com/taubyte-test/tb_code_test_project │
	// ├─────────────┼──────────────────────────────────────────────────────┤
	// │             │ Config                                               │
	// │ Name:       │ taubyte-test/tb_test_project                         │
	// │ URL:        │ https://github.com/taubyte-test/tb_test_project      │
	// └─────────────┴──────────────────────────────────────────────────────┘
}
