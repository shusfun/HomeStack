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
		if cleaned != "." && cleaned != "" {
			info, err := fs.Stat(assets, cleaned)
			if err == nil && !info.IsDir() {
				files.ServeHTTP(writer, request)
				return
			}
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				http.Error(writer, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		cloned := request.Clone(request.Context())
		cloned.URL.Path = "/"
		cloned.URL.RawPath = ""
		files.ServeHTTP(writer, cloned)
	})
}
