package managed

import "time"

const (
	PhasePending     = "pending"
	PhaseSelecting   = "selecting"
	PhaseDownloading = "downloading"
	PhaseVerifying   = "verifying"
	PhaseExtracting  = "extracting"
	PhaseInstalling  = "installing"
	PhaseReady       = "ready"
	PhaseError       = "error"
	PhaseCancelled   = "cancelled"
)

type Progress struct {
	Component  string `json:"component"`
	Version    string `json:"version,omitempty"`
	Phase      string `json:"phase"`
	Downloaded int64  `json:"downloaded,omitempty"`
	Total      int64  `json:"total,omitempty"`
	SpeedBPS   int64  `json:"speed_bps,omitempty"`
	SourceHost string `json:"source_host,omitempty"`
	Error      string `json:"error,omitempty"`
}

type ProgressFunc func(Progress)

type progressWriter struct {
	written  int64
	total    int64
	started  time.Time
	lastSent time.Time
	report   func(downloaded, speed int64)
}

func (w *progressWriter) Write(data []byte) (int, error) {
	count := len(data)
	w.written += int64(count)
	now := time.Now()
	if w.report != nil && (w.lastSent.IsZero() || now.Sub(w.lastSent) >= 200*time.Millisecond || w.written == w.total) {
		elapsed := now.Sub(w.started).Seconds()
		speed := int64(0)
		if elapsed > 0 {
			speed = int64(float64(w.written) / elapsed)
		}
		w.report(w.written, speed)
		w.lastSent = now
	}
	return count, nil
}
