package components

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/wangshangbin/homestack/internal/protocol"
)

type Spec struct {
	ID              string
	Binary          string
	VersionArgs     []string
	ExpectedVersion string
	VersionPattern  *regexp.Regexp
}

var FixedSpecs = []Spec{
	{ID: "headscale", Binary: "headscale", VersionArgs: []string{"version"}, ExpectedVersion: "0.29.3", VersionPattern: regexp.MustCompile(`\bv?0\.29\.3\b`)},
	{ID: "tailscale", Binary: "tailscale", VersionArgs: []string{"version"}, ExpectedVersion: "1.102.2", VersionPattern: regexp.MustCompile(`\b1\.102\.2\b`)},
	{ID: "pocket-id", Binary: "pocket-id", VersionArgs: []string{"version"}, ExpectedVersion: "2.12.0", VersionPattern: regexp.MustCompile(`\bv?2\.12\.0\b`)},
	{ID: "filebrowser", Binary: "filebrowser", VersionArgs: []string{"version"}, ExpectedVersion: "0.3.5", VersionPattern: regexp.MustCompile(`\bv?0\.3\.5\b`)},
	{ID: "jellyfin", Binary: "jellyfin", VersionArgs: []string{"--version"}, ExpectedVersion: "10.11.11", VersionPattern: regexp.MustCompile(`\b10\.11\.11\b`)},
	{ID: "cc-connect", Binary: "cc-connect", VersionArgs: []string{"--version"}, ExpectedVersion: "1.4.1", VersionPattern: regexp.MustCompile(`\bv?1\.4\.1\b`)},
}

func Check(ctx context.Context, spec Spec) protocol.ModuleStatusV1 {
	checkedAt := time.Now().UTC()
	path, err := exec.LookPath(spec.Binary)
	if err != nil {
		return protocol.ModuleStatusV1{ID: spec.ID, State: "missing", ExpectedVersion: spec.ExpectedVersion, Detail: err.Error(), CheckedAt: checkedAt}
	}
	command := exec.CommandContext(ctx, path, spec.VersionArgs...)
	output, err := command.CombinedOutput()
	if err != nil {
		return protocol.ModuleStatusV1{ID: spec.ID, State: "error", ExpectedVersion: spec.ExpectedVersion, Detail: commandError(err, output), CheckedAt: checkedAt}
	}
	versionText := strings.TrimSpace(string(output))
	if !spec.VersionPattern.MatchString(versionText) {
		return protocol.ModuleStatusV1{ID: spec.ID, State: "version_mismatch", Version: versionText, ExpectedVersion: spec.ExpectedVersion, Detail: "检测到的版本与固定版本不一致", CheckedAt: checkedAt}
	}
	return protocol.ModuleStatusV1{ID: spec.ID, State: "ready", Version: spec.ExpectedVersion, ExpectedVersion: spec.ExpectedVersion, CheckedAt: checkedAt}
}

func RequireVersion(ctx context.Context, spec Spec) error {
	status := Check(ctx, spec)
	if status.State != "ready" {
		return fmt.Errorf("组件 %s 不可用: %s", spec.ID, status.Detail)
	}
	return nil
}

func FindSpec(id string) (Spec, error) {
	for _, spec := range FixedSpecs {
		if spec.ID == id {
			return spec, nil
		}
	}
	return Spec{}, errors.New("未知外部组件")
}

func commandError(err error, output []byte) string {
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return err.Error()
	}
	return err.Error() + ": " + detail
}
