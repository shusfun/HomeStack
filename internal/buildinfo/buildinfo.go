package buildinfo

import (
	"encoding/json"
	"fmt"
	"runtime"
)

var (
	Version                = "dev"
	Commit                 = "unknown"
	Date                   = "unknown"
	UpdateManifestURL      = "https://github.com/shusfun/HomeStack/releases/latest/download/latest.json"
	UpdatePublicKey        = ""
	AgentUpdateManifestURL = "https://github.com/shusfun/HomeStack/releases/latest/download/latest.json"
)

func String(name string) string {
	return fmt.Sprintf("%s %s (commit %s, built %s)", name, Version, Commit, Date)
}

func Requested(arguments []string) bool {
	return len(arguments) == 1 && (arguments[0] == "version" || arguments[0] == "--version" || arguments[0] == "-v" || arguments[0] == "--version-json")
}

func Output(name string, arguments []string) string {
	if len(arguments) == 1 && arguments[0] == "--version-json" {
		data, _ := json.Marshal(map[string]string{
			"name": name, "version": Version, "goos": runtime.GOOS, "goarch": runtime.GOARCH,
			"commit": Commit, "built_at": Date,
		})
		return string(data)
	}
	return String(name)
}
