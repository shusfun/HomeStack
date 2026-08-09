package protocol

import "time"

type NodeRegistration struct {
	Name                string            `json:"name"`
	Platform            string            `json:"platform"`
	Architecture        string            `json:"architecture"`
	TailscaleIP         string            `json:"tailscale_ip"`
	MagicDNS            string            `json:"magic_dns"`
	DevicePublicKey     string            `json:"device_public_key"`
	EncryptionPublicKey string            `json:"encryption_public_key"`
	Modules             []ModuleConfig    `json:"modules"`
	SharedDirectories   []SharedDirectory `json:"shared_directories,omitempty"`
}

type RegistrationResponse struct {
	DeviceID         string         `json:"device_id"`
	DeviceName       string         `json:"device_name"`
	SealedCredential SealedEnvelope `json:"sealed_credential"`
	SignedConfig     string         `json:"signed_config"`
}

type SealedEnvelope struct {
	EphemeralPublicKey string `json:"ephemeral_public_key"`
	Nonce              string `json:"nonce"`
	Ciphertext         string `json:"ciphertext"`
}

type DeviceCredential struct {
	DeviceID      string                       `json:"device_id"`
	DeviceToken   string                       `json:"device_token"`
	ModuleSecrets map[string]map[string]string `json:"module_secrets,omitempty"`
	ExpiresAt     time.Time                    `json:"expires_at"`
}

type SharedDirectory struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

type ModuleConfig struct {
	ID         string `json:"id"`
	InstanceID string `json:"instance_id,omitempty"`
	Enabled    bool   `json:"enabled"`
	BaseURL    string `json:"base_url,omitempty"`
	WorkDir    string `json:"work_dir,omitempty"`
	ReadOnly   bool   `json:"read_only"`
}

type SignedDeviceConfig struct {
	DeviceID          string            `json:"device_id"`
	DeviceName        string            `json:"device_name"`
	Revision          uint64            `json:"revision"`
	IssuedAt          time.Time         `json:"issued_at"`
	ExpiresAt         time.Time         `json:"expires_at"`
	ControlURL        string            `json:"control_url"`
	AgentURL          string            `json:"agent_url"`
	Modules           []ModuleConfig    `json:"modules"`
	SharedDirectories []SharedDirectory `json:"shared_directories"`
}

type ModuleStatus struct {
	ID              string    `json:"id"`
	State           string    `json:"state"`
	Version         string    `json:"version,omitempty"`
	ExpectedVersion string    `json:"expected_version"`
	Detail          string    `json:"detail,omitempty"`
	CheckedAt       time.Time `json:"checked_at"`
}

type CapabilityStatus struct {
	ID     string `json:"id"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

type DeviceStatus struct {
	DeviceID       string             `json:"device_id"`
	Name           string             `json:"name"`
	Online         bool               `json:"online"`
	TailscaleIP    string             `json:"tailscale_ip,omitempty"`
	Connection     string             `json:"connection"`
	LastSeen       time.Time          `json:"last_seen"`
	ConfigRevision uint64             `json:"config_revision"`
	Modules        []ModuleStatus     `json:"modules"`
	Capabilities   []CapabilityStatus `json:"capabilities"`
}

type AccessTicketClaims struct {
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
