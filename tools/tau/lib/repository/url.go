package repositoryLib

import "fmt"

func GetRepositoryUrl(provider, fullName string) string {
	switch provider {
	case "github":
		return "https://github.com/" + fullName
	default:
		panic(fmt.Sprintf("url for provider: `%s` not found", provider))
	}
}
