// Package live runs the forge drivers against REAL forge servers in
// containers: Gitea, Forgejo, GitLab and OneDev, at pinned versions.
//
// It has no code of its own. Its tests are tagged docker_integration and run
// with `make test-forges`; untagged, the package is this comment and nothing
// else, so `make test` passes over it.
//
// WHAT THE CONFORMANCE SUITE CANNOT SAY. forgetest runs every driver against a
// fixture written from the forge's documentation, which proves the driver
// agrees with the fixture. Whether the fixture agrees with the forge — that a
// Gitea really signs X-Hub-Signature-256 over the bytes it sends, that a deploy
// key it installs really refuses a push, that the clone URL it answers with is
// one a build can clone — only a forge can say. Each test here boots one,
// makes its accounts with the server's own CLI or API, and walks one
// repository through everything tau asks of a forge: who the credential is,
// what it may do, create, list, a deploy key, a hook, a real push delivering a
// real signed webhook verified by pkg/forge, a clone with the deploy key
// through services/build/deploy's transport, and deletion.
package live
