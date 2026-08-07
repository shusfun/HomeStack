package setup

import (
	"errors"
	"strings"

	"github.com/wangshangbin/homestack/internal/publicurl"
)

func NormalizePrepareRequest(request PrepareRequest) (Configuration, error) {
	address, err := publicurl.Normalize(request.PublicHost)
	if err != nil {
		return Configuration{}, errors.New("VPS 域名无效: " + err.Error())
	}
	provider := strings.ToLower(strings.TrimSpace(request.Provider))
	credentials := ProviderCredentials{ClientID: strings.TrimSpace(request.ClientID), ClientSecret: strings.TrimSpace(request.ClientSecret)}
	config := Configuration{PublicHost: address.Host, Providers: map[string]ProviderCredentials{provider: credentials}}
	if err := ValidateConfiguration(config); err != nil {
		return Configuration{}, err
	}
	return config, nil
}

func NormalizeConfiguration(config Configuration) (Configuration, error) {
	address, err := publicurl.Normalize(config.PublicHost)
	if err != nil {
		return Configuration{}, errors.New("VPS 域名无效: " + err.Error())
	}
	normalized := Configuration{PublicHost: address.Host, Providers: map[string]ProviderCredentials{}}
	for provider, credentials := range config.Providers {
		normalized.Providers[strings.ToLower(strings.TrimSpace(provider))] = ProviderCredentials{ClientID: strings.TrimSpace(credentials.ClientID), ClientSecret: strings.TrimSpace(credentials.ClientSecret)}
	}
	if err := ValidateConfiguration(normalized); err != nil {
		return Configuration{}, err
	}
	return normalized, nil
}

func ValidateConfiguration(config Configuration) error {
	if _, err := publicurl.Normalize(config.PublicHost); err != nil {
		return errors.New("VPS 域名无效: " + err.Error())
	}
	if len(config.Providers) == 0 || len(config.Providers) > 2 {
		return errors.New("必须至少完整配置一种登录方式")
	}
	for provider, credentials := range config.Providers {
		if provider != "google" && provider != "github" {
			return errors.New("登录方式只能是 Google 或 GitHub")
		}
		if credentials.ClientID == "" || credentials.ClientSecret == "" {
			return errors.New("OAuth Client ID 和 Client Secret 必须完整填写")
		}
		if strings.ContainsAny(credentials.ClientID, "\r\n\x00") || strings.ContainsAny(credentials.ClientSecret, "\r\n\x00") {
			return errors.New("OAuth 凭据包含非法控制字符")
		}
	}
	return nil
}

func PublicConfigurationFor(config Configuration) PublicConfiguration {
	result := PublicConfiguration{PublicHost: config.PublicHost}
	for _, id := range []string{"google", "github"} {
		if credentials, ok := config.Providers[id]; ok {
			result.Providers = append(result.Providers, PublicProviderConfiguration{ID: id, ClientID: credentials.ClientID})
		}
	}
	return result
}
