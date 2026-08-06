package protocol

import "time"

const (
	JoinVersion         = "homestack.join.v1"
	DeviceConfigVersion = "homestack.device-config.v1"
	DeviceStatusVersion = "homestack.device-status.v1"
	AccessTicketVersion = "homestack.access-ticket.v1"
)

type JoinDescriptorV1 struct {
	Version string `json:"version"`
	Server  string `json:"server"`
	Code    string `json:"code"`
}

type JoinRequestV1 struct {
	Version             string `json:"version"`
	Code                string `json:"code"`
	DeviceName          string `json:"device_name"`
	AgentURL            string `json:"agent_url"`
	EncryptionPublicKey string `json:"encryption_public_key"`
}

type JoinResponseV1 struct {
	Version          string           `json:"version"`
	DeviceID         string           `json:"device_id"`
	DeviceName       string           `json:"device_name"`
	SealedCredential SealedEnvelopeV1 `json:"sealed_credential"`
	SignedConfig     string           `json:"signed_config"`
}

type SealedEnvelopeV1 struct {
	Version            string `json:"version"`
	EphemeralPublicKey string `json:"ephemeral_public_key"`
	Nonce              string `json:"nonce"`
	Ciphertext         string `json:"ciphertext"`
}

type DeviceCredentialV1 struct {
	Version              string                       `json:"version"`
	DeviceID             string                       `json:"device_id"`
	DeviceToken          string                       `json:"device_token"`
	HeadscaleLoginServer string                       `json:"headscale_login_server"`
	HeadscaleAuthKey     string                       `json:"headscale_auth_key"`
	ModuleSecrets        map[string]map[string]string `json:"module_secrets,omitempty"`
	ExpiresAt            time.Time                    `json:"expires_at"`
}

type JoinPolicyV1 struct {
	DeviceName        string                       `json:"device_name"`
	AgentURL          string                       `json:"agent_url"`
	Modules           []ModuleConfigV1             `json:"modules"`
	SharedDirectories []SharedDirectoryV1          `json:"shared_directories"`
	ModuleSecrets     map[string]map[string]string `json:"module_secrets,omitempty"`
}

type SharedDirectoryV1 struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
}

type ModuleConfigV1 struct {
	ID         string `json:"id"`
	InstanceID string `json:"instance_id,omitempty"`
	Enabled    bool   `json:"enabled"`
	BaseURL    string `json:"base_url,omitempty"`
	WorkDir    string `json:"work_dir,omitempty"`
	ReadOnly   bool   `json:"read_only"`
}

type SignedDeviceConfigV1 struct {
	Version           string              `json:"version"`
	DeviceID          string              `json:"device_id"`
	DeviceName        string              `json:"device_name"`
	Revision          uint64              `json:"revision"`
	IssuedAt          time.Time           `json:"issued_at"`
	ExpiresAt         time.Time           `json:"expires_at"`
	ControlURL        string              `json:"control_url"`
	AgentURL          string              `json:"agent_url"`
	Modules           []ModuleConfigV1    `json:"modules"`
	SharedDirectories []SharedDirectoryV1 `json:"shared_directories"`
}

type ModuleStatusV1 struct {
	ID              string    `json:"id"`
	State           string    `json:"state"`
	Version         string    `json:"version,omitempty"`
	ExpectedVersion string    `json:"expected_version"`
	Detail          string    `json:"detail,omitempty"`
	CheckedAt       time.Time `json:"checked_at"`
}

type DeviceStatusV1 struct {
	Version        string           `json:"version"`
	DeviceID       string           `json:"device_id"`
	Name           string           `json:"name"`
	Online         bool             `json:"online"`
	TailnetIP      string           `json:"tailnet_ip,omitempty"`
	Connection     string           `json:"connection"`
	DERPRegion     string           `json:"derp_region,omitempty"`
	LastSeen       time.Time        `json:"last_seen"`
	ConfigRevision uint64           `json:"config_revision"`
	Modules        []ModuleStatusV1 `json:"modules"`
}

type AccessTicketClaimsV1 struct {
	Version   string    `json:"version"`
	Issuer    string    `json:"iss"`
	Subject   string    `json:"sub"`
	DeviceID  string    `json:"device_id"`
	Nonce     string    `json:"nonce"`
	IssuedAt  time.Time `json:"iat"`
	ExpiresAt time.Time `json:"exp"`
}

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ErrorResponse struct {
	Error APIError `json:"error"`
}
