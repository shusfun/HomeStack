package maintenance

import (
	"context"
	"time"

	setupapi "github.com/wangshangbin/homestack/internal/setup"
)

const DefaultSocketPath = "/run/homestack-maintenance/helper.sock"

type Configuration = setupapi.Configuration

type Phase string

const (
	PhaseIdle      Phase = "idle"
	PhasePreflight Phase = "preflight"
	PhaseApplying  Phase = "applying"
	PhaseRollback  Phase = "rollback"
	PhaseCompleted Phase = "completed"
	PhaseFailed    Phase = "failed"
)

type Status struct {
	Phase     Phase          `json:"phase"`
	Current   *Configuration `json:"current,omitempty"`
	Target    *Configuration `json:"target,omitempty"`
	TargetURL string         `json:"target_url,omitempty"`
	Error     string         `json:"error,omitempty"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type Helper interface {
	Configuration(context.Context) (Configuration, error)
	Status(context.Context) (Status, error)
	Reconfigure(context.Context, Configuration) (Status, error)
}

type Request struct {
	Operation string         `json:"operation"`
	Config    *Configuration `json:"config,omitempty"`
}

type Response struct {
	Config *Configuration `json:"config,omitempty"`
	Status Status         `json:"status"`
	Error  string         `json:"error,omitempty"`
}
