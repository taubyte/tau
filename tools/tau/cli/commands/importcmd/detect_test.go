package importcmd

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/importer/compose"
	"github.com/taubyte/tau/pkg/importer/fly"
	"github.com/taubyte/tau/pkg/importer/lambda"
	"github.com/taubyte/tau/pkg/importer/vercel"
)

const composeFile = "services:\n  api:\n    image: x\n"

// TestTheNameNarrowsAndTheContentDecides.
func TestFormatDetection(t *testing.T) {
	for _, c := range []struct {
		name string
		path string
		data string
		want string
	}{
		{"a compose file by name", "docker-compose.yml", composeFile, compose.Source},
		{"the other spelling", "compose.yaml", composeFile, compose.Source},
		{"an override file", "docker-compose.prod.yml", composeFile, compose.Source},
		{"vercel by name", "vercel.json", `{"buildCommand":"x"}`, vercel.Source},
		{"fly by name", "fly.toml", "app = \"x\"\n", fly.Source},

		// A file whose name says nothing.
		{"compose by content", "stack.yml", composeFile, compose.Source},
		{"json is vercel's", "anything.json", `{"buildCommand":"x"}`, vercel.Source},
		{"a SAM template", "template.yaml", "Transform: AWS::Serverless-2016-10-31\n", lambda.Source},
		{"a serverless manifest", "serverless.yml", "service: shop\nprovider:\n  runtime: nodejs20.x\n", lambda.Source},

		// SERVICES IS COMPOSE'S AND SERVICE IS SERVERLESS FRAMEWORK'S, one
		// letter apart, and reading one as the other produces an import of
		// nothing with no error.
		{"services is not service", "x.yml", "services:\n  api:\n    image: x\n", compose.Source},
		{"functions without services", "x.yml", "functions:\n  api:\n    handler: h\n", lambda.Source},

		{"nothing recognisable", "notes.txt", "hello\n", ""},
		{"not yaml at all", "notes.txt", "\tthis: [is not: valid", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, guessFrom(c.path, []byte(c.data)))
		})
	}
}

// TestCoolifyAndDokployAreNeverGuessed. Both hand their user's compose file to
// real `docker compose`, so their files are indistinguishable from a plain one
// — and what tells them apart is which platform the person is leaving, which is
// not in the file. Guessing one would attach findings about an API nobody has.
func TestCoolifyAndDokployAreNeverGuessed(t *testing.T) {
	require.Equal(t, compose.Source, guessFrom("docker-compose.yml", []byte(composeFile)))

	// They are still reachable by name.
	r, err := importOf("coolify", "docker-compose.yml", []byte(composeFile))
	require.NoError(t, err)
	require.Equal(t, "coolify", r.Source)

	r, err = importOf("dokploy", "docker-compose.yml", []byte(composeFile))
	require.NoError(t, err)
	require.Equal(t, "dokploy", r.Source)
}

func TestAnUnrecognisedFileSaysWhatWasTried(t *testing.T) {
	_, err := importOf("", "notes.txt", []byte("hello"))
	require.ErrorIs(t, err, ErrUnrecognised)
	require.Contains(t, err.Error(), "compose")
	require.Contains(t, err.Error(), "--from")
}

func TestAnUnknownFormatIsRefused(t *testing.T) {
	_, err := importOf("heroku", "app.json", []byte("{}"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "heroku")
	require.Contains(t, err.Error(), "vercel", "and it lists what tau does read")
}

// TestNamingTheFormatSkipsTheGuess, so a compose file called anything at all
// still imports.
func TestNamingTheFormatSkipsTheGuess(t *testing.T) {
	r, err := importOf("compose", "some-random-name", []byte(composeFile))
	require.NoError(t, err)
	require.Equal(t, compose.Source, r.Source)
}
