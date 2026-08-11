package desktop

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wangshangbin/homestack/internal/protocol"
	"github.com/wangshangbin/homestack/internal/securestore"
	"github.com/zalando/go-keyring"
)

func TestSessionIgnoresManagedContentFailure(t *testing.T) {
	prepareDesktopSession(t)
	if err := keyring.Set("HomeStack", "managed-content", "{"); err != nil {
		t.Fatal(err)
	}

	status, err := NewService().Session()
	if err != nil {
		t.Fatalf("托管内容损坏不应阻止读取激活状态: %v", err)
	}
	if !status.LoggedIn || status.ControlURL != "https://control.example.com" {
		t.Fatalf("激活状态错误: %+v", status)
	}
}

func TestManagedPreparationStartsOnceAndPreservesSessionOnFailure(t *testing.T) {
	prepareDesktopSession(t)
	service := NewService()
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var runs atomic.Int32
	service.managedRunner = func(context.Context) error {
		runs.Add(1)
		started <- struct{}{}
		<-release
		return errors.New("组件下载失败")
	}

	first, err := service.EnsureManagedContentPreparation()
	if err != nil || first.State != "preparing" {
		t.Fatalf("首次启动组件任务失败: status=%+v err=%v", first, err)
	}
	second, err := service.EnsureManagedContentPreparation()
	if err != nil || second.State != "preparing" {
		t.Fatalf("幂等启动组件任务失败: status=%+v err=%v", second, err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("组件任务未启动")
	}
	if runs.Load() != 1 {
		t.Fatalf("组件任务重复启动: %d", runs.Load())
	}
	close(release)
	waitManagedState(t, service, "error")

	session, err := service.Session()
	if err != nil || !session.LoggedIn {
		t.Fatalf("组件失败后激活状态丢失: status=%+v err=%v", session, err)
	}
}

func TestLogoutCancelsManagedPreparation(t *testing.T) {
	prepareDesktopSession(t)
	service := NewService()
	stopped := make(chan struct{}, 1)
	service.managedRunner = func(ctx context.Context) error {
		<-ctx.Done()
		stopped <- struct{}{}
		return ctx.Err()
	}
	if _, err := service.EnsureManagedContentPreparation(); err != nil {
		t.Fatal(err)
	}
	if err := service.Logout(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("退出登录未取消组件任务")
	}
	status, err := service.Session()
	if err != nil || status.LoggedIn {
		t.Fatalf("退出后仍为已登录: status=%+v err=%v", status, err)
	}
}

func TestLogoutWaitsForManagedPreparationToStopBeforeDeletingSession(t *testing.T) {
	prepareDesktopSession(t)
	service := NewService()
	release := make(chan struct{})
	service.managedRunner = func(ctx context.Context) error {
		<-ctx.Done()
		<-release
		return ctx.Err()
	}
	if _, err := service.EnsureManagedContentPreparation(); err != nil {
		t.Fatal(err)
	}

	finished := make(chan error, 1)
	go func() { finished <- service.Logout() }()
	select {
	case err := <-finished:
		t.Fatalf("后台任务退出前 Logout 不应返回: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	status, err := service.Session()
	if err != nil || !status.LoggedIn {
		t.Fatalf("后台任务退出前不应删除 Session: status=%+v err=%v", status, err)
	}

	close(release)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("后台任务退出后 Logout 未完成")
	}
}

func TestCancelledManagedPreparationKeepsSessionAndCanResume(t *testing.T) {
	prepareDesktopSession(t)
	service := NewService()
	started := make(chan struct{}, 1)
	resumed := make(chan struct{}, 1)
	var runs atomic.Int32
	service.managedRunner = func(ctx context.Context) error {
		if runs.Add(1) == 1 {
			started <- struct{}{}
			<-ctx.Done()
			return ctx.Err()
		}
		resumed <- struct{}{}
		return nil
	}
	if _, err := service.EnsureManagedContentPreparation(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("组件任务未启动")
	}
	if err := service.CancelManagedContentPreparation(); err != nil {
		t.Fatal(err)
	}
	waitManagedStopped(t, service)
	session, err := service.Session()
	if err != nil || !session.LoggedIn {
		t.Fatalf("取消组件准备后激活状态丢失: status=%+v err=%v", session, err)
	}
	if _, err := service.ResumeManagedContentPreparation(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-resumed:
	case <-time.After(time.Second):
		t.Fatal("组件任务未恢复")
	}
	waitManagedState(t, service, "ready")
}

func prepareDesktopSession(t *testing.T) {
	t.Helper()
	keyring.MockInit()
	now := time.Now().UTC()
	if err := securestore.SaveAppSession(securestore.AppSession{
		ControlURL: "https://control.example.com", AccessToken: "access", RefreshToken: "refresh",
		AccessExpiresAt: now.Add(time.Hour), RefreshExpiresAt: now.Add(24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := securestore.SaveDeviceProfile(securestore.DeviceProfile{
		DeviceID: "device-1", DeviceName: "Windows", ControlKeyID: "key-1",
		ControlPublicKey: "public-key", SignedConfig: "signed-config",
		Credential: protocol.DeviceCredential{DeviceID: "device-1", DeviceToken: "device-token", ExpiresAt: now.Add(24 * time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}
}

func waitManagedState(t *testing.T, service *Service, expected string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if service.ManagedContentStatus().State == expected {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("组件状态未变为 %s: %+v", expected, service.ManagedContentStatus())
}

func waitManagedStopped(t *testing.T, service *Service) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		service.managedStatusMu.RLock()
		stopped := service.managedCancel == nil
		service.managedStatusMu.RUnlock()
		if stopped {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("组件任务未停止")
}
