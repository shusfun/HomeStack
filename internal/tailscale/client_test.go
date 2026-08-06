package tailscale

import (
	"strings"
	"testing"
)

func TestValidateDERPMapAcceptsOnlySelfHostedRegion(t *testing.T) {
	valid := []byte(`{"Regions":{"900":{"RegionID":900,"RegionCode":"homestack","RegionName":"HomeStack"}},"omitDefaultRegions":true}`)
	if err := validateDERPMap(valid); err != nil {
		t.Fatalf("自有 DERP map 应通过校验: %v", err)
	}
}

func TestValidateDERPMapRejectsPublicOrAdditionalRegions(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		message string
	}{
		{name: "公共区域", payload: `{"Regions":{"1":{"RegionCode":"nyc"}}}`, message: "非自有 DERP"},
		{name: "额外区域", payload: `{"Regions":{"900":{"RegionCode":"homestack"},"901":{"RegionCode":"backup"}}}`, message: "只能包含一个"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateDERPMap([]byte(test.payload))
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("期望错误包含 %q，实际为 %v", test.message, err)
			}
		})
	}
}
