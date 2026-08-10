package managed

import (
	"strings"
	"testing"
)

func TestValidateProfileRejectsLegacySchema(t *testing.T) {
	err := ValidateProfile(Profile{})
	if err == nil || !strings.Contains(err.Error(), "schema 不受支持: 0") {
		t.Fatalf("旧托管内容档案未触发重新初始化: %v", err)
	}
}
