// Package live runs the forge drivers against real forge servers in containers
// (Gitea, Forgejo, GitLab, Gerrit, OneDev, Gogs) at pinned versions. Its tests
// are tagged docker_integration and run with `make test-forges`.
//
// forgetest proves a driver agrees with a fixture written from the docs; only a
// real forge proves the fixture agrees with the forge. Each test boots one,
// makes accounts with the server's own CLI or API, and walks one repository
// through everything tau asks: identity, access, create, list, a deploy key, a
// hook, a real push with a signed webhook, a clone with the deploy key, and
// deletion.
package live
