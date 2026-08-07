package setup

import "time"

const (
	DefaultSocketPath = "/run/homestack-setup/helper.sock"
	SessionCookieName = "homestack_setup_session"
)

type Phase string

const (
	PhaseToken          Phase = "token"
	PhaseInfrastructure Phase = "infrastructure"
	PhasePocketID       Phase = "pocket-id"
	PhaseFinalize       Phase = "finalize"
	PhaseCompleted      Phase = "completed"
)

type Configuration struct {
	ControlHost string `json:"control_host"`
	PocketHost  string `json:"pocket_host"`
	MeshHost    string `json:"mesh_host"`
	TailHost    string `json:"tail_host"`
	PublicIPv4  string `json:"public_ipv4"`
}

type Status struct {
	Surface   string         `json:"surface"`
	Phase     Phase          `json:"phase"`
	Config    *Configuration `json:"config,omitempty"`
	PocketURL string         `json:"pocket_url,omitempty"`
	Error     string         `json:"error,omitempty"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type HelperRequest struct {
	Operation string         `json:"operation"`
	Config    *Configuration `json:"config,omitempty"`
}

type HelperResponse struct {
	Status Status `json:"status"`
	Error  string `json:"error,omitempty"`
}
