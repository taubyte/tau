package auth

import "github.com/google/go-github/v71/github"

// Caller is the part of a git client that says who is asking. It is the whole
// input an identity provider gets: enough to identify the caller, and nothing
// with which to act on their behalf.
type Caller interface {
	Me() *github.User
}
