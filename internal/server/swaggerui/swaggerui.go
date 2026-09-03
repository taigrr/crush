// Package swaggerui serves an embedded Swagger UI. It bundles only the
// static assets the UI actually loads (no source maps, no unused ES
// bundles), keeping the binary small while preserving the full docs UI.
package swaggerui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/swaggo/swag"
)

//go:embed dist
var dist embed.FS

// Handler serves the Swagger UI under the given prefix (e.g. "/v1/docs/").
// It serves the OpenAPI spec at "<prefix>doc.json" from the registered
// swag spec and the static UI assets for every other path.
func Handler(prefix string) http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	files := http.StripPrefix(prefix, http.FileServer(http.FS(sub)))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, prefix) {
		case "doc.json", "swagger.json":
			doc, err := swag.ReadDoc()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(doc))
		case "", "index.html":
			data, err := fs.ReadFile(sub, "index.html")
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(data)
		default:
			files.ServeHTTP(w, r)
		}
	})
}
