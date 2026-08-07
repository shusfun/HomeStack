package control

import (
	"context"
	"errors"
	"testing"

	setupapi "github.com/wangshangbin/homestack/internal/setup"
)

type providerLinkHelper struct{ err error }

func (h providerLinkHelper) Configuration(context.Context) (setupapi.PublicConfiguration, error) {
	return setupapi.PublicConfiguration{}, nil
}
func (h providerLinkHelper) ReconfigureDomain(context.Context, string) (setupapi.Status, error) {
	return setupapi.Status{}, nil
}
func (h providerLinkHelper) LinkProvider(context.Context, string, setupapi.ProviderCredentials) (setupapi.Status, error) {
	return setupapi.Status{}, h.err
}

func TestProviderLinkServicePreservesExistingIdentityAndRollsBackFailure(t *testing.T) {
	owners, _ := OpenOwnerStore("")
	owner, _ := owners.AuthenticateOrClaim(ExternalIdentity{Provider: "github", Subject: "github-owner", Email: "owner@example.com", EmailVerified: true})
	google := ExternalIdentity{Provider: "google", Subject: "google-owner", Email: "other@example.com", EmailVerified: true}
	service := providerLinkService{owners: owners, helper: providerLinkHelper{}}
	if err := service.LinkProvider(context.Background(), owner.Subject, google, setupapi.ProviderCredentials{ClientID: "id", ClientSecret: "secret"}); err != nil {
		t.Fatal(err)
	}
	if _, err := owners.AuthenticateOrClaim(google); err != nil {
		t.Fatalf("新身份未绑定: %v", err)
	}
	if _, err := owners.AuthenticateOrClaim(ExternalIdentity{Provider: "github", Subject: "github-owner", Email: "owner@example.com", EmailVerified: true}); err != nil {
		t.Fatalf("旧身份失效: %v", err)
	}

	failedOwners, _ := OpenOwnerStore("")
	failedOwner, _ := failedOwners.AuthenticateOrClaim(ExternalIdentity{Provider: "github", Subject: "github-owner", Email: "owner@example.com", EmailVerified: true})
	failure := providerLinkService{owners: failedOwners, helper: providerLinkHelper{err: errors.New("write failed")}}
	if err := failure.LinkProvider(context.Background(), failedOwner.Subject, google, setupapi.ProviderCredentials{ClientID: "id", ClientSecret: "secret"}); err == nil {
		t.Fatal("配置失败必须向上传播")
	}
	if _, err := failedOwners.AuthenticateOrClaim(google); !errors.Is(err, ErrIdentityNotLinked) {
		t.Fatalf("配置失败后身份未回滚: %v", err)
	}
}
