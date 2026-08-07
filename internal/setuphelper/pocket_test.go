package setuphelper

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	setupapi "github.com/wangshangbin/homestack/internal/setup"
)

func TestPocketBootstrapCreatesRestrictedClientsAndIsRetrySafe(t *testing.T) {
	var mu sync.Mutex
	groupCreated := false
	clients := map[string]bool{}
	secrets := map[string]string{}
	var memberships []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-API-Key") != "temporary-key" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch {
		case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/api/users"):
			writePocketJSON(writer, map[string]any{"data": []map[string]any{{"id": staticAPIUserID, "isAdmin": true}, {"id": "admin-1", "isAdmin": true, "disabled": false, "userGroups": []map[string]string{{"id": "existing-group"}}, "futureField": "ignored"}}, "pagination": map[string]any{"total": 2}, "futureEnvelope": true})
		case request.Method == http.MethodGet && request.URL.Path == "/api/user-groups":
			data := []map[string]string{}
			if groupCreated {
				data = append(data, map[string]string{"id": "group-1", "name": "homestack-users"})
			}
			writePocketJSON(writer, map[string]any{"data": data})
		case request.Method == http.MethodPost && request.URL.Path == "/api/user-groups":
			groupCreated = true
			writer.WriteHeader(http.StatusCreated)
			writePocketJSON(writer, map[string]string{"id": "group-1"})
		case request.Method == http.MethodPut && strings.Contains(request.URL.Path, "/user-groups"):
			var body struct {
				UserGroupIDs []string `json:"userGroupIds"`
			}
			_ = json.NewDecoder(request.Body).Decode(&body)
			memberships = body.UserGroupIDs
			writePocketJSON(writer, map[string]string{"id": "admin-1"})
		case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/api/oidc/clients/"):
			id := strings.TrimPrefix(request.URL.Path, "/api/oidc/clients/")
			if !clients[id] {
				http.Error(writer, "missing", http.StatusNotFound)
				return
			}
			writePocketJSON(writer, map[string]any{"id": id, "name": "Existing", "description": "managed", "callbackURLs": []string{"https://old.example.com/callback"}, "logoutCallbackURLs": []string{}, "isPublic": false, "pkceEnabled": true, "requiresReauthentication": false, "requiresPushedAuthorizationRequests": false, "skipConsent": true, "credentials": map[string]any{}, "isGroupRestricted": true, "futureField": "ignored"})
		case request.Method == http.MethodPost && request.URL.Path == "/api/oidc/clients":
			var body struct {
				ID string `json:"id"`
			}
			_ = json.NewDecoder(request.Body).Decode(&body)
			clients[body.ID] = true
			writer.WriteHeader(http.StatusCreated)
			writePocketJSON(writer, map[string]string{"id": body.ID})
		case request.Method == http.MethodPut && strings.HasPrefix(request.URL.Path, "/api/oidc/clients/"):
			writer.WriteHeader(http.StatusNoContent)
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/secret"):
			var body struct {
				Secret string `json:"secret"`
			}
			_ = json.NewDecoder(request.Body).Decode(&body)
			secrets[request.URL.Path] = body.Secret
			writer.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("未处理 Pocket API 请求: %s %s", request.Method, request.URL.Path)
			http.Error(writer, "unexpected", http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := pocketClient{baseURL: server.URL, apiKey: "temporary-key", client: server.Client()}
	admin, err := client.initialAdmin(context.Background())
	if err != nil || admin.ID != "admin-1" {
		t.Fatalf("识别初始管理员失败: %+v %v", admin, err)
	}
	config := setupapi.Configuration{ControlHost: "app.example.com", MeshHost: "mesh.example.com"}
	for attempt := 0; attempt < 2; attempt++ {
		groupID, err := client.createGroup(context.Background(), admin)
		if err != nil || groupID != "group-1" {
			t.Fatalf("创建用户组失败: %s %v", groupID, err)
		}
		if err := client.createOIDCClients(context.Background(), config, groupID, "control-secret-123456", "headscale-secret-123456"); err != nil {
			t.Fatal(err)
		}
	}
	if len(clients) != 2 || len(secrets) != 2 {
		t.Fatalf("OIDC 初始化不完整: clients=%v secrets=%v", clients, secrets)
	}
	if len(memberships) != 2 || memberships[0] != "existing-group" || memberships[1] != "group-1" {
		t.Fatalf("管理员已有 Pocket 用户组未保留: %v", memberships)
	}
}

func TestRemoveStaticAPIKey(t *testing.T) {
	directory := t.TempDir()
	envPath := filepath.Join(directory, "pocket.env")
	keyPath := filepath.Join(directory, "key")
	if err := os.WriteFile(envPath, []byte("APP_URL=https://id.example.com\nSTATIC_API_KEY_FILE="+keyPath+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeStaticAPIKey(envPath, keyPath); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "STATIC_API_KEY") {
		t.Fatal("临时 API Key 配置未删除")
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatal("临时 API Key 文件未删除")
	}
}

func writePocketJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(value)
}
