package projectLib

import "strings"

func CleanGitURL(apiURL string) string {
	apiURL = strings.ReplaceAll(apiURL, "/repos", "")
	apiURL = strings.ReplaceAll(apiURL, "api.", "")

	return apiURL
}
