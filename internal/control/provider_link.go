package control

import (
	"context"
	"fmt"

	setupapi "github.com/wangshangbin/homestack/internal/setup"
)

type providerLinkService struct {
	owners *OwnerStore
	helper setupapi.ConfigHelper
}

func (s providerLinkService) LinkProvider(ctx context.Context, ownerID string, external ExternalIdentity, credentials setupapi.ProviderCredentials) error {
	if s.owners == nil || s.helper == nil {
		return fmt.Errorf("登录方式绑定依赖未配置")
	}
	if err := s.owners.AddIdentity(ownerID, external); err != nil {
		return err
	}
	if _, err := s.helper.LinkProvider(ctx, external.Provider, credentials); err != nil {
		key := IdentityKey{Provider: external.Provider, Subject: external.Subject}
		if rollbackErr := s.owners.RemoveIdentity(ownerID, key); rollbackErr != nil {
			return fmt.Errorf("%v；恢复 Owner 身份失败: %w", err, rollbackErr)
		}
		return err
	}
	return nil
}

func providerLabel(provider string) string {
	if provider == "google" {
		return "Google"
	}
	return "GitHub"
}
