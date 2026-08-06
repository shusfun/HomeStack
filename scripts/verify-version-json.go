//go:build ignore

package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	var value struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		GOOS    string `json:"goos"`
		GOARCH  string `json:"goarch"`
	}
	if err := json.Unmarshal([]byte(os.Getenv("METADATA")), &value); err != nil {
		fmt.Fprintln(os.Stderr, "版本 JSON 无效:", err)
		os.Exit(1)
	}
	if value.Name != os.Getenv("EXPECTED_NAME") || value.Version != os.Getenv("EXPECTED_VERSION") || value.GOOS != os.Getenv("EXPECTED_OS") || value.GOARCH != os.Getenv("EXPECTED_ARCH") {
		fmt.Fprintf(os.Stderr, "内嵌版本不匹配: %+v\n", value)
		os.Exit(1)
	}
}
