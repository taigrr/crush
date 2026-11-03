package devicescope

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFromSwagger(t *testing.T) {
	t.Parallel()
	doc := `{"basePath": "/v1", "paths": {
		"/a/{id}": {"get": {"x-crush-scope": "sessions:read"}, "post": {"x-crush-scope": "never"}, "parameters": []},
		"/b": {"delete": {"x-crush-scope": "shell"}}
	}}`
	routes, err := FromSwagger([]byte(doc))
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"GET /v1/a/{id}": SessionsRead, "POST /v1/a/{id}": Never, "DELETE /v1/b": Shell,
	}, routes)

	src := string(Source("server", routes))
	require.Contains(t, src, `"GET /v1/a/{id}": "sessions:read",`)
	require.Contains(t, src, "DO NOT EDIT")
}

func TestFromSwaggerRejectsMissingOrUnknownScopes(t *testing.T) {
	t.Parallel()
	for name, doc := range map[string]string{
		"missing":      `{"basePath": "/v1", "paths": {"/a": {"get": {"summary": "x"}}}}`,
		"unknown":      `{"basePath": "/v1", "paths": {"/a": {"get": {"x-crush-scope": "root"}}}}`,
		"not a string": `{"basePath": "/v1", "paths": {"/a": {"get": {"x-crush-scope": ["sessions:read"]}}}}`,
	} {
		_, err := FromSwagger([]byte(doc))
		require.ErrorIs(t, err, ErrUnannotated, name)
		require.ErrorContains(t, err, "GET /v1/a", name)
	}
	_, err := FromSwagger([]byte(`{"paths": {}}`))
	require.Error(t, err)
	_, err = FromSwagger([]byte(`nope`))
	require.Error(t, err)
}
