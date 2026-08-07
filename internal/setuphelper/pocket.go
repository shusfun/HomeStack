package setuphelper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	setupapi "github.com/wangshangbin/homestack/internal/setup"
)

type pocketClient struct {
	baseURL, apiKey string
	client          *http.Client
}

type pocketHTTPError struct {
	Status int
	Body   string
}

func (e *pocketHTTPError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.Status, e.Body) }

type pocketUser struct {
	ID         string `json:"id"`
	IsAdmin    bool   `json:"isAdmin"`
	Disabled   bool   `json:"disabled"`
	UserGroups []struct {
		ID string `json:"id"`
	} `json:"userGroups"`
}

type pocketOIDCClientRequest struct {
	Name                                string         `json:"name"`
	Description                         string         `json:"description"`
	CallbackURLs                        []string       `json:"callbackURLs"`
	LogoutCallbackURLs                  []string       `json:"logoutCallbackURLs"`
	IsPublic                            bool           `json:"isPublic"`
	PKCEEnabled                         bool           `json:"pkceEnabled"`
	RequiresReauthentication            bool           `json:"requiresReauthentication"`
	RequiresPushedAuthorizationRequests bool           `json:"requiresPushedAuthorizationRequests"`
	SkipConsent                         bool           `json:"skipConsent"`
	Credentials                         map[string]any `json:"credentials"`
	IsGroupRestricted                   bool           `json:"isGroupRestricted"`
}

func (p pocketClient) initialAdmin(ctx context.Context) (pocketUser, error) {
	var response struct {
		Data []pocketUser `json:"data"`
	}
	if err := p.request(ctx, http.MethodGet, "/api/users?pagination[limit]=20", nil, &response); err != nil {
		return pocketUser{}, fmt.Errorf("读取 Pocket ID 用户失败: %w", err)
	}
	admins := make([]pocketUser, 0, 1)
	for _, value := range response.Data {
		if value.ID != staticAPIUserID && value.IsAdmin && !value.Disabled {
			admins = append(admins, value)
		}
	}
	if len(admins) != 1 {
		return pocketUser{}, fmt.Errorf("Pocket ID 必须恰好存在一个已启用的真实管理员，当前为 %d", len(admins))
	}
	return admins[0], nil
}

func (p pocketClient) createGroup(ctx context.Context, admin pocketUser) (string, error) {
	var group struct {
		ID string `json:"id"`
	}
	var groups struct {
		Data []struct{ ID, Name string } `json:"data"`
	}
	if err := p.request(ctx, http.MethodGet, "/api/user-groups?search=homestack-users&pagination[limit]=20", nil, &groups); err != nil {
		return "", fmt.Errorf("查询 Pocket ID 用户组失败: %w", err)
	}
	for _, existing := range groups.Data {
		if existing.Name == "homestack-users" {
			group.ID = existing.ID
			break
		}
	}
	if group.ID == "" {
		if err := p.request(ctx, http.MethodPost, "/api/user-groups", map[string]any{"friendlyName": "HomeStack Users", "name": "homestack-users"}, &group); err != nil {
			return "", fmt.Errorf("创建 Pocket ID 用户组失败: %w", err)
		}
	}
	if group.ID == "" {
		return "", errors.New("Pocket ID 创建用户组未返回 ID")
	}
	groupIDs := make([]string, 0, len(admin.UserGroups)+1)
	seen := map[string]bool{}
	for _, existing := range admin.UserGroups {
		if existing.ID != "" && !seen[existing.ID] {
			seen[existing.ID] = true
			groupIDs = append(groupIDs, existing.ID)
		}
	}
	if !seen[group.ID] {
		groupIDs = append(groupIDs, group.ID)
	}
	if err := p.request(ctx, http.MethodPut, "/api/users/"+url.PathEscape(admin.ID)+"/user-groups", map[string]any{"userGroupIds": groupIDs}, nil); err != nil {
		return "", fmt.Errorf("加入 Pocket ID 用户组失败: %w", err)
	}
	return group.ID, nil
}

func (p pocketClient) createOIDCClients(ctx context.Context, config setupapi.Configuration, groupID, controlSecret, headscaleSecret string) error {
	clients := []struct{ id, name, callback, secret string }{
		{"homestack-control", "HomeStack", "https://" + config.ControlHost + "/auth/callback/pocket", controlSecret},
		{"homestack-headscale", "HomeStack Headscale", "https://" + config.MeshHost + "/oidc/callback", headscaleSecret},
	}
	for _, item := range clients {
		body := pocketOIDCClientRequest{Name: item.name, Description: "HomeStack managed client", CallbackURLs: []string{item.callback}, LogoutCallbackURLs: []string{}, PKCEEnabled: true, SkipConsent: true, Credentials: map[string]any{}, IsGroupRestricted: true}
		err := p.request(ctx, http.MethodGet, "/api/oidc/clients/"+item.id, nil, nil)
		var httpError *pocketHTTPError
		if errors.As(err, &httpError) && httpError.Status == http.StatusNotFound {
			createBody := struct {
				ID string `json:"id"`
				pocketOIDCClientRequest
			}{ID: item.id, pocketOIDCClientRequest: body}
			if err := p.request(ctx, http.MethodPost, "/api/oidc/clients", createBody, nil); err != nil {
				return fmt.Errorf("创建 Pocket ID 客户端 %s 失败: %w", item.id, err)
			}
		} else if err != nil {
			return fmt.Errorf("查询 Pocket ID 客户端 %s 失败: %w", item.id, err)
		} else {
			if err := p.request(ctx, http.MethodPut, "/api/oidc/clients/"+item.id, body, nil); err != nil {
				return fmt.Errorf("更新 Pocket ID 客户端 %s 失败: %w", item.id, err)
			}
		}
		if err := p.request(ctx, http.MethodPost, "/api/oidc/clients/"+item.id+"/secret", map[string]string{"secret": item.secret}, nil); err != nil {
			return fmt.Errorf("设置 Pocket ID 客户端 %s 密钥失败: %w", item.id, err)
		}
		if err := p.request(ctx, http.MethodPut, "/api/oidc/clients/"+item.id+"/allowed-user-groups", map[string]any{"userGroupIds": []string{groupID}}, nil); err != nil {
			return fmt.Errorf("限制 Pocket ID 客户端 %s 用户组失败: %w", item.id, err)
		}
	}
	return nil
}

func (p pocketClient) updateClientCallbacks(ctx context.Context, id string, callbacks []string) error {
	var body pocketOIDCClientRequest
	if err := p.request(ctx, http.MethodGet, "/api/oidc/clients/"+url.PathEscape(id), nil, &body); err != nil {
		return fmt.Errorf("读取 Pocket ID 客户端 %s 失败: %w", id, err)
	}
	if body.Credentials == nil {
		body.Credentials = map[string]any{}
	}
	body.CallbackURLs = callbacks
	if err := p.request(ctx, http.MethodPut, "/api/oidc/clients/"+url.PathEscape(id), body, nil); err != nil {
		return fmt.Errorf("更新 Pocket ID 客户端 %s 回调失败: %w", id, err)
	}
	return nil
}

func (p pocketClient) request(ctx context.Context, method, path string, body, target any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(p.baseURL, "/")+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("X-API-Key", p.apiKey)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := p.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		return &pocketHTTPError{Status: response.StatusCode, Body: strings.TrimSpace(string(data))}
	}
	if target == nil || response.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(target)
}
