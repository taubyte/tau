package js_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	js "github.com/taubyte/tau/pkg/invoke/js"
	"github.com/taubyte/tau/pkg/invoke/js/archive"
)

// THE CORPUS'S JS SOURCE ANSWERS BEFORE ANY BUNDLER TOUCHES IT. An archive holds
// modules, so the fixture's two files load as the two modules they are, and
// what the corpus asserts of the bundled function is asserted here without
// docker: the method and the body, whole.
func TestTheCorpusFixtureAnswersTheMethodAndTheBody(t *testing.T) {
	files := map[string][]byte{}
	for _, name := range []string{"src/index.js", "src/answer.js"} {
		body, err := os.ReadFile(filepath.Join("fixtures", "answer", filepath.FromSlash(name)))
		require.NoError(t, err)
		files[name] = body
	}
	var packed bytes.Buffer
	require.NoError(t, archive.Write(&packed, "src/index.js", files))

	assets := &store{}
	assets.put("answer", packed.Bytes())
	tier, err := js.New(assets)
	require.NoError(t, err)
	instance, err := tier.Start(context.Background(), invoke.Spec{Runtime: js.Name, Name: "shop/prod/function/answer", Artifact: "answer"})
	require.NoError(t, err)
	t.Cleanup(func() { instance.Close() })

	posted := serve(t, instance, &invoke.Request{Method: "POST", Path: "/", Body: []byte("ping")})
	require.Equal(t, 200, posted.Status)
	require.Equal(t, "POST ping", string(posted.Body))
	require.Equal(t, "GET ", string(serve(t, instance, get("/orders")).Body))
}
