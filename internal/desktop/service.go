package desktop

import (
	"context"
	"errors"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wangshangbin/homestack/internal/securestore"
	"github.com/wangshangbin/homestack/internal/tailscale"
)

type Service struct {
	joiner *Joiner
}

type LocalStatus struct {
	Configured bool   `json:"configured"`
	DeviceID   string `json:"device_id,omitempty"`
	DeviceName string `json:"device_name,omitempty"`
	Online     bool   `json:"online"`
	TailnetIP  string `json:"tailnet_ip,omitempty"`
	Connection string `json:"connection"`
	Error      string `json:"error,omitempty"`
}

func NewService() *Service {
	return &Service{joiner: &Joiner{OpenURL: func(target string) error {
		app := application.Get()
		if app == nil || app.Browser == nil {
			return errors.New("Wails 浏览器服务尚未初始化")
		}
		return app.Browser.OpenURL(target)
	}}}
}

func (s *Service) Join(connectionInfo string) (JoinResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
	defer cancel()
	return s.joiner.Join(ctx, connectionInfo)
}

func (s *Service) LocalStatus() LocalStatus {
	status := LocalStatus{Connection: "未连接"}
	profile, err := securestore.LoadDeviceProfile()
	if err == nil {
		status.Configured = true
		status.DeviceID = profile.DeviceID
		status.DeviceName = profile.DeviceName
	}
	client, err := tailscale.New()
	if err != nil {
		status.Error = err.Error()
		return status
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.VerifyVersion(ctx); err != nil {
		status.Error = err.Error()
		return status
	}
	tailnet, err := client.Status(ctx)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.Online = tailnet.Online
	status.TailnetIP = tailnet.TailnetIP
	status.Connection = tailnet.Connection
	return status
}
