package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/wangshangbin/homestack/internal/protocol"
)

type ControlClient struct {
	BaseURL     string
	DeviceID    string
	DeviceToken string
	HTTPClient  *http.Client
}

func (c *ControlClient) PostStatus(ctx context.Context, status protocol.DeviceStatusV1) error {
	body, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("编码设备状态失败: %w", err)
	}
	endpoint := c.BaseURL + "/api/v1/devices/" + url.PathEscape(c.DeviceID) + "/status"
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	c.authorize(request)
	response, err := c.client().Do(request)
	if err != nil {
		return fmt.Errorf("上报设备状态失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return controlResponseError("上报设备状态", response)
	}
	return nil
}

func (c *ControlClient) RefreshConfig(ctx context.Context, store *ConfigStore) error {
	endpoint := c.BaseURL + "/api/v1/device/config?device_id=" + url.QueryEscape(c.DeviceID)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	c.authorize(request)
	response, err := c.client().Do(request)
	if err != nil {
		return fmt.Errorf("拉取设备配置失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return controlResponseError("拉取设备配置", response)
	}
	var payload struct {
		SignedConfig string `json:"signed_config"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return fmt.Errorf("解析设备配置响应失败: %w", err)
	}
	if payload.SignedConfig == store.Signed() {
		return nil
	}
	_, err = store.Apply(payload.SignedConfig)
	return err
}

func (c *ControlClient) authorize(request *http.Request) {
	request.Header.Set("Authorization", "Device "+c.DeviceToken)
}

func (c *ControlClient) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func controlResponseError(action string, response *http.Response) error {
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%s失败，HTTP %d，且读取错误响应失败: %w", action, response.StatusCode, err)
	}
	var payload protocol.ErrorResponse
	if json.Unmarshal(data, &payload) == nil && payload.Error.Message != "" {
		return fmt.Errorf("%s失败，HTTP %d: %s", action, response.StatusCode, payload.Error.Message)
	}
	return fmt.Errorf("%s失败，HTTP %d: %s", action, response.StatusCode, string(data))
}
