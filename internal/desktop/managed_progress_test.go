package desktop

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wangshangbin/homestack/internal/managed"
)

func TestCancelManagedContentPreparationPreservesCompletedComponents(t *testing.T) {
	service := NewService()
	cancelled := make(chan struct{})
	service.beginManagedPreparation(func() { close(cancelled) })
	service.reportManagedProgress(managed.Progress{Component: "filebrowser", Version: managed.FileBrowserVersion, Phase: managed.PhaseReady, Downloaded: 10, Total: 10})
	service.reportManagedProgress(managed.Progress{Component: "jellyfin", Version: managed.JellyfinVersion, Phase: managed.PhaseDownloading, Downloaded: 2, Total: 10})
	service.reportManagedProgress(managed.Progress{Component: "jellyfin-ffmpeg", Version: managed.FFmpegVersion, Phase: managed.PhasePending, Total: 10})
	if err := service.CancelManagedContentPreparation(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("取消函数未被调用")
	}
	status := service.ManagedContentStatus()
	if status.State != "cancelled" || status.Total != 30 || status.Downloaded != 12 || status.Components[0].Phase != managed.PhaseReady || status.Components[1].Phase != managed.PhaseCancelled {
		t.Fatalf("取消状态错误: %+v", status)
	}
}

func TestWaitNodeHealthAtAcceptsHealthyNode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	if err := waitNodeHealthAt(context.Background(), server.Client(), server.URL); err != nil {
		t.Fatal(err)
	}
}

func TestManagedNodeStageOwnsConfigurationError(t *testing.T) {
	service := NewService()
	service.beginManagedPreparation(func() {})
	service.setManagedStage("configuring")
	service.finishManagedPreparation("error", "error", "配置失败")
	status := service.ManagedContentStatus()
	if status.State != "error" || status.Components[3].Phase != managed.PhaseError || status.Components[3].Error != "配置失败" {
		t.Fatalf("Node 配置错误未归属到 Node 行: %+v", status)
	}
}

func TestWaitNodeHealthAtReturnsTimeoutError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := waitNodeHealthAt(ctx, server.Client(), server.URL); err == nil {
		t.Fatal("Node 健康检查超时未返回错误")
	}
}
