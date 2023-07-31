package fixtures

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/google/go-github/github"
	"github.com/taubyte/tau/libdream/common"
	commonTest "github.com/taubyte/tau/libdream/helpers"
	dreamlandRegistry "github.com/taubyte/tau/libdream/registry"
	"golang.org/x/oauth2"
)

func init() {
	dreamlandRegistry.Fixture("clearRepos", clearRepos)
}

func clearRepos(u common.Universe, params ...interface{}) error {
	if len(params) > 0 {
		return errors.New("parameters are unused")
	}
	client := githubApiClient(u, commonTest.GitToken)
	repos, _, err := client.Repositories.List(u.Context(), "", &github.RepositoryListOptions{
		ListOptions: github.ListOptions{PerPage: 1000},
	})
	if err != nil {
		return fmt.Errorf("listing repositories failed with")
	}

	res, err := client.Repositories.DownloadContents(context.Background(), commonTest.GitUser, "tb_testProject", "keep_repos.txt", nil)
	if err != nil {
		fmt.Println("failed with:", err)
	}
	defer res.Close()

	body, err := io.ReadAll(res)
	if err != nil {
		return err
	}

	keepRepos := strings.Split(string(body), "\n")
	if len(keepRepos) < 4 {
		return fmt.Errorf("not enough repos")
	}

	for _, repo := range repos {
		name := *repo.Name

		var found bool
		for _, _name := range keepRepos {
			if _name == name {
				found = true
				break
			}
		}

		if found {
			fmt.Printf("Listing keys -- %s/%s\n", commonTest.GitUser, name)
			keys, gitResponse, err := client.Repositories.ListKeys(u.Context(), commonTest.GitUser, name, &github.ListOptions{PerPage: 1000})
			if err != nil {
				return fmt.Errorf("listing keys for %s failed with: %w", name, err)
			}
			fmt.Printf("Listing keys RESP %#v\n", gitResponse)

			for {
				for _, key := range keys {
					if strings.Contains(*key.Title, "_dev") || *key.Title == "go-simple-git-clone-with-deploy-key" {
						gitResponse, err = client.Repositories.DeleteKey(u.Context(), commonTest.GitUser, name, key.GetID())
						fmt.Println("Deleting", *key.Title, ":", gitResponse)
						if err != nil {
							return fmt.Errorf("deleting key for %s: `%s`, failed with: %w", name, *key.Title, err)
						}
					}
				}

				fmt.Printf("Listing keys again -- %s/%s\n", commonTest.GitUser, name)
				keys, gitResponse, err = client.Repositories.ListKeys(u.Context(), commonTest.GitUser, name, &github.ListOptions{PerPage: 1000})
				if err != nil {
					return fmt.Errorf("listing keys for %s failed with: %w", name, err)
				}
				fmt.Printf("Listing keys again RESP %#v\n", gitResponse)

				deletableKeys := []string{}
				for _, key := range keys {
					if strings.Contains(*key.Title, "_dev") || *key.Title == "go-simple-git-clone-with-deploy-key" {
						deletableKeys = append(deletableKeys, *key.Title)
					}
				}

				if len(deletableKeys) == 0 {
					break
				}
			}
		} else {
			fmt.Printf("DELETING %s/%s \n", commonTest.GitUser, name)
			_, err = client.Repositories.Delete(u.Context(), commonTest.GitUser, name)
			if err != nil {
				return fmt.Errorf("deleting %s failed with: %w", name, err)
			}
		}
	}

	return nil
}

func githubApiClient(u common.Universe, token string) *github.Client {
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(u.Context(), ts)

	return github.NewClient(tc)
}
