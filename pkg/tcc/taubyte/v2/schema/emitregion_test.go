package schema_test

import (
	"context"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// validating compiles the documents and returns the deferred validations the
// compile asked its caller to run.
func validating(t *testing.T, docs map[string]string) []schema.NextValidation {
	t.Helper()

	fs := afero.NewMemMapFs()
	for path, body := range docs {
		require.NoError(t, afero.WriteFile(fs, path, []byte(body), 0o644))
	}
	out, err := schema.Validate(context.Background(),
		schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	require.NoError(t, err)
	return out
}

func regionsAsked(v []schema.NextValidation) []string {
	var out []string
	for _, one := range v {
		if one.Validator == "region" {
			out = append(out, one.Value.(string))
		}
	}
	return out
}

const projectID = "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nenvironments:\n    prod:\n        branches: [\"*\"]\n"

func TestARegionLabelIsHandedToTheCaller(t *testing.T) {
	asked := regionsAsked(validating(t, map[string]string{
		"/project.yaml": projectID + "regions:\n    topology: active\n    in: [region-1, region-2]\n",
		"/namespace/shop/meta.yaml": "id: QmQPeNsJPyVWPFDVHb77w8G42Fvo15z4bmmjRoBHfW1YZv\n" +
			"regions:\n    in: [region-3]\n",
	}))
	require.ElementsMatch(t, []string{"region-1", "region-2", "region-3"}, asked,
		"a nested, optional, REPEATED label was not carried, on the project or a namespace")
}

func TestAnAbsentBlockAsksNothing(t *testing.T) {
	require.Empty(t, regionsAsked(validating(t, map[string]string{
		"/project.yaml": projectID,
		"/secret/s.yaml": "id: QmVs4sQjPFqJZ7Ug5MPpFxPTNiZ1CT8xY4XPKUbnDNbNAY\n" +
			"source: value\nrotation: 720h\n",
	})))
}
