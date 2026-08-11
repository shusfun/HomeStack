package web

import (
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var embedded embed.FS

func Assets() fs.FS {
	assets, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err)
	}
	return assets
}

func Handler() http.Handler {
	assets := Assets()
	files := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			http.Error(writer, "只允许读取静态资源", http.StatusMethodNotAllowed)
			return
		}
		cleaned := strings.TrimPrefix(path.Clean("/"+request.URL.Path), "/")
		if cleaned == "index.html" {
			writer.Header().Set("Cache-Control", "no-store")
			cloned := request.Clone(request.Context())
			cloned.URL.Path = "/"
			cloned.URL.RawPath = ""
			files.ServeHTTP(writer, cloned)
			return
		}
		if cleaned != "." && cleaned != "" {
			info, err := fs.Stat(assets, cleaned)
			if err == nil && !info.IsDir() {
				setCachePolicy(writer, cleaned)
				files.ServeHTTP(writer, request)
				return
			}
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				http.Error(writer, err.Error(), http.StatusInternalServerError)
				return
			}
			if strings.HasPrefix(cleaned, "assets/") || path.Ext(cleaned) != "" {
				http.NotFound(writer, request)
				return
			}
		}
		writer.Header().Set("Cache-Control", "no-store")
		cloned := request.Clone(request.Context())
		cloned.URL.Path = "/"
		cloned.URL.RawPath = ""
		files.ServeHTTP(writer, cloned)
	})
}

func setCachePolicy(writer http.ResponseWriter, name string) {
	if name == "index.html" || name == "assets/app.js" || name == "assets/app.css" {
		writer.Header().Set("Cache-Control", "no-store")
		return
	}
	if strings.HasPrefix(name, "assets/") {
		writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
}
