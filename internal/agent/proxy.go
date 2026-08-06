package agent

import (
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

var fileBrowserReadRoutes = map[string]struct{}{
	"/api/resources": {},
	"/api/raw":       {},
	"/api/preview":   {},
	"/api/search":    {},
	"/api/usage":     {},
}

func NewFileBrowserProxy(rawBaseURL, apiToken string) (http.Handler, error) {
	target, err := parseLoopbackTarget(rawBaseURL)
	if err != nil {
		return nil, err
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1
	originalDirector := proxy.Director
	proxy.Director = func(request *http.Request) {
		originalDirector(request)
		request.Header.Del("Cookie")
		request.Header.Del("Authorization")
		request.Header.Set("Authorization", "Bearer "+apiToken)
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			writeAgentError(writer, http.StatusMethodNotAllowed, "read_only", "FileBrowser 仅允许读取和下载")
			return
		}
		if _, ok := fileBrowserReadRoutes[request.URL.Path]; !ok {
			writeAgentError(writer, http.StatusForbidden, "route_denied", "FileBrowser API 不在只读白名单中")
			return
		}
		if err := validateRequestPaths(request.URL); err != nil {
			writeAgentError(writer, http.StatusBadRequest, "invalid_path", err.Error())
			return
		}
		proxy.ServeHTTP(writer, request)
	}), nil
}

func NewJellyfinProxy(rawBaseURL, apiKey string) (http.Handler, error) {
	target, err := parseLoopbackTarget(rawBaseURL)
	if err != nil {
		return nil, err
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1
	originalDirector := proxy.Director
	proxy.Director = func(request *http.Request) {
		originalDirector(request)
		request.Header.Del("Cookie")
		request.Header.Del("Authorization")
		request.Header.Set("X-Emby-Token", apiKey)
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !jellyfinRequestAllowed(request) {
			writeAgentError(writer, http.StatusForbidden, "route_denied", "Jellyfin API 不在媒体白名单中")
			return
		}
		if err := validateRequestPaths(request.URL); err != nil {
			writeAgentError(writer, http.StatusBadRequest, "invalid_path", err.Error())
			return
		}
		proxy.ServeHTTP(writer, request)
	}), nil
}

func jellyfinRequestAllowed(request *http.Request) bool {
	path := strings.ToLower(request.URL.Path)
	if request.Method == http.MethodGet || request.Method == http.MethodHead {
		return path == "/items" || strings.HasPrefix(path, "/items/") || path == "/users" || strings.HasPrefix(path, "/users/") ||
			strings.HasPrefix(path, "/videos/") || strings.HasPrefix(path, "/audio/") || strings.HasPrefix(path, "/livevideos/") ||
			path == "/system/info/public" || path == "/branding/configuration"
	}
	if request.Method == http.MethodPost {
		if path == "/sessions/playing" || path == "/sessions/playing/progress" || path == "/sessions/playing/stopped" {
			return true
		}
		parts := strings.Split(strings.Trim(path, "/"), "/")
		return len(parts) == 3 && parts[0] == "items" && parts[1] != "" && parts[2] == "playbackinfo"
	}
	return false
}

func parseLoopbackTarget(raw string) (*url.URL, error) {
	if err := requireLoopbackURL(raw); err != nil {
		return nil, err
	}
	return url.Parse(raw)
}

func validateRequestPaths(requestURL *url.URL) error {
	if err := rejectTraversal(requestURL.EscapedPath()); err != nil {
		return err
	}
	for _, values := range requestURL.Query() {
		for _, value := range values {
			if strings.ContainsAny(value, "/\\") {
				if err := rejectTraversal(value); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func rejectTraversal(raw string) error {
	decoded := raw
	for range 3 {
		next, err := url.PathUnescape(decoded)
		if err != nil {
			return errors.New("路径编码无效")
		}
		if next == decoded {
			break
		}
		decoded = next
	}
	decoded = strings.ReplaceAll(decoded, "\\", "/")
	if strings.ContainsRune(decoded, '\x00') {
		return errors.New("路径包含空字符")
	}
	for _, segment := range strings.Split(decoded, "/") {
		if segment == ".." {
			return errors.New("拒绝路径穿越")
		}
	}
	return nil
}

func newStreamingServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
}
