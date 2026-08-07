package setup

import "time"

const (
	DefaultSocketPath = "/run/homestack-config/helper.sock"
	SessionCookieName = "homestack_setup_session"
)

type Phase string

const (
	PhaseToken     Phase = "token"
	PhaseDomain    Phase = "domain"
	PhaseIdentity  Phase = "identity"
	PhaseFinalize  Phase = "finalize"
	PhaseCompleted Phase = "completed"
)

type Configuration struct {
	PublicHost   string `json:"public_host"`
	Provider     string `json:"provider"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

type PublicConfiguration struct {
	PublicHost string `json:"public_host"`
	Provider   string `json:"provider"`
	ClientID   string `json:"client_id"`
}

type Status struct {
	Surface   string               `json:"surface"`
	Phase     Phase                `json:"phase"`
	Config    *PublicConfiguration `json:"config,omitempty"`
	Error     string               `json:"error,omitempty"`
	UpdatedAt time.Time            `json:"updated_at"`
}

type HelperRequest struct {
	Operation string         `json:"operation"`
	Config    *Configuration `json:"config,omitempty"`
}

type HelperResponse struct {
	Status Status `json:"status"`
	Error  string `json:"error,omitempty"`
}
