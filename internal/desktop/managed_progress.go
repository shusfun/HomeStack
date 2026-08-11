package desktop

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wangshangbin/homestack/internal/managed"
)

const managedProgressEvent = "homestack:managed-content-progress"

type ManagedComponentStatus struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Version    string `json:"version,omitempty"`
	Phase      string `json:"phase"`
	Downloaded int64  `json:"downloaded,omitempty"`
	Total      int64  `json:"total,omitempty"`
	SpeedBPS   int64  `json:"speed_bps,omitempty"`
	SourceHost string `json:"source_host,omitempty"`
	Error      string `json:"error,omitempty"`
}

type ManagedContentStatus struct {
	State      string                   `json:"state"`
	Phase      string                   `json:"phase"`
	Downloaded int64                    `json:"downloaded,omitempty"`
	Total      int64                    `json:"total,omitempty"`
	SpeedBPS   int64                    `json:"speed_bps,omitempty"`
	Error      string                   `json:"error,omitempty"`
	Components []ManagedComponentStatus `json:"components"`
}

func newManagedContentStatus() ManagedContentStatus {
	return ManagedContentStatus{State: "idle", Phase: "idle", Components: []ManagedComponentStatus{
		{ID: "filebrowser", Label: "FileBrowser", Version: managed.FileBrowserVersion, Phase: managed.PhasePending},
		{ID: "jellyfin", Label: "Jellyfin", Version: managed.JellyfinVersion, Phase: managed.PhasePending},
		{ID: "jellyfin-ffmpeg", Label: "Jellyfin FFmpeg", Version: managed.FFmpegVersion, Phase: managed.PhasePending},
		{ID: "node", Label: "HomeStack Node", Phase: managed.PhasePending},
	}}
}

func (s *Service) ManagedContentStatus() ManagedContentStatus {
	s.managedStatusMu.RLock()
	defer s.managedStatusMu.RUnlock()
	return cloneManagedStatus(s.managedStatus)
}

func (s *Service) CancelManagedContentPreparation() error {
	s.managedStatusMu.Lock()
	cancel := s.managedCancel
	if cancel == nil || s.managedStatus.State != "preparing" {
		s.managedStatusMu.Unlock()
		return errors.New("当前没有可取消的组件准备任务")
	}
	cancel()
	s.managedStatus.State = "cancelled"
	s.managedStatus.Phase = "cancelled"
	s.managedStatus.Error = "组件准备已取消"
	if index := slices.IndexFunc(s.managedStatus.Components, func(item ManagedComponentStatus) bool {
		return item.Phase != managed.PhaseReady && item.Phase != managed.PhasePending
	}); index >= 0 {
		s.managedStatus.Components[index].Phase = managed.PhaseCancelled
	}
	status := cloneManagedStatus(s.managedStatus)
	s.managedStatusMu.Unlock()
	emitManagedStatus(status)
	return nil
}

func (s *Service) beginManagedPreparation(cancel context.CancelFunc) {
	s.managedStatusMu.Lock()
	status := newManagedContentStatus()
	status.State = "preparing"
	status.Phase = "manifest"
	s.managedStatus = status
	s.managedCancel = cancel
	current := cloneManagedStatus(status)
	s.managedStatusMu.Unlock()
	emitManagedStatus(current)
}

func (s *Service) finishManagedPreparation(state, phase, detail string) {
	s.managedStatusMu.Lock()
	s.managedCancel = nil
	if s.managedStatus.State == "cancelled" && state == "error" {
		current := cloneManagedStatus(s.managedStatus)
		s.managedStatusMu.Unlock()
		emitManagedStatus(current)
		return
	}
	s.managedStatus.State = state
	s.managedStatus.Phase = phase
	s.managedStatus.Error = detail
	if state == "error" {
		index := slices.IndexFunc(s.managedStatus.Components, func(item ManagedComponentStatus) bool {
			return item.Phase != managed.PhaseReady && item.Phase != managed.PhasePending && item.Phase != managed.PhaseError
		})
		if index < 0 {
			index = slices.IndexFunc(s.managedStatus.Components, func(item ManagedComponentStatus) bool { return item.Phase == managed.PhaseError })
		}
		if index >= 0 {
			s.managedStatus.Components[index].Phase = managed.PhaseError
			s.managedStatus.Components[index].Error = detail
		}
	}
	current := cloneManagedStatus(s.managedStatus)
	s.managedStatusMu.Unlock()
	emitManagedStatus(current)
}

func (s *Service) setManagedStage(phase string) {
	s.managedStatusMu.Lock()
	s.managedStatus.State = "preparing"
	s.managedStatus.Phase = phase
	if index := slices.IndexFunc(s.managedStatus.Components, func(item ManagedComponentStatus) bool { return item.ID == "node" }); index >= 0 {
		s.managedStatus.Components[index].Phase = phase
	}
	current := cloneManagedStatus(s.managedStatus)
	s.managedStatusMu.Unlock()
	emitManagedStatus(current)
}

func (s *Service) beginManagedHealthCheck(profile managed.Profile, cancel context.CancelFunc) {
	status := newManagedContentStatus()
	status.State, status.Phase = "preparing", "health"
	installations := []managed.Installation{profile.FileBrowser, profile.Jellyfin, profile.FFmpeg}
	for index, installation := range installations {
		status.Components[index].Phase = managed.PhaseReady
		status.Components[index].SourceHost = installation.SourceHost
	}
	status.Components[3].Phase = "health"
	s.managedStatusMu.Lock()
	s.managedStatus = status
	s.managedCancel = cancel
	current := cloneManagedStatus(status)
	s.managedStatusMu.Unlock()
	emitManagedStatus(current)
}

func (s *Service) reportManagedProgress(progress managed.Progress) {
	s.managedStatusMu.Lock()
	if s.managedStatus.State == "cancelled" {
		s.managedStatusMu.Unlock()
		return
	}
	index := slices.IndexFunc(s.managedStatus.Components, func(item ManagedComponentStatus) bool { return item.ID == progress.Component })
	if index >= 0 {
		item := &s.managedStatus.Components[index]
		item.Version = progress.Version
		item.Phase = progress.Phase
		if progress.Phase != managed.PhaseError || progress.Downloaded > 0 {
			item.Downloaded = progress.Downloaded
		}
		item.Total = progress.Total
		item.SpeedBPS = progress.SpeedBPS
		item.SourceHost = progress.SourceHost
		item.Error = progress.Error
	}
	s.managedStatus.Phase = progress.Phase
	s.managedStatus.Downloaded = 0
	s.managedStatus.Total = 0
	s.managedStatus.SpeedBPS = 0
	for _, item := range s.managedStatus.Components[:3] {
		s.managedStatus.Downloaded += item.Downloaded
		s.managedStatus.Total += item.Total
		s.managedStatus.SpeedBPS += item.SpeedBPS
	}
	current := cloneManagedStatus(s.managedStatus)
	s.managedStatusMu.Unlock()
	emitManagedStatus(current)
}

func (s *Service) setManagedReadyFromProfile(profile managed.Profile) {
	status := newManagedContentStatus()
	status.State, status.Phase = "ready", "ready"
	installations := []managed.Installation{profile.FileBrowser, profile.Jellyfin, profile.FFmpeg}
	for index, installation := range installations {
		status.Components[index].Phase = managed.PhaseReady
		status.Components[index].SourceHost = installation.SourceHost
	}
	status.Components[3].Phase = managed.PhaseReady
	s.managedStatusMu.Lock()
	s.managedStatus = status
	current := cloneManagedStatus(status)
	s.managedStatusMu.Unlock()
	emitManagedStatus(current)
}

func waitNodeHealth(ctx context.Context) error {
	client := &http.Client{Timeout: 2 * time.Second}
	return waitNodeHealthAt(ctx, client, "http://127.0.0.1:19444/api/health")
}

func waitNodeHealthAt(ctx context.Context, client *http.Client, endpoint string) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return errors.New("HomeStack Node 在限定时间内未通过健康检查")
		case <-ticker.C:
		}
	}
}

func cloneManagedStatus(status ManagedContentStatus) ManagedContentStatus {
	status.Components = append([]ManagedComponentStatus(nil), status.Components...)
	return status
}

func emitManagedStatus(status ManagedContentStatus) {
	if app := application.Get(); app != nil {
		app.Event.Emit(managedProgressEvent, status)
	}
}
