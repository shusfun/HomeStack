//go:build linux

package agent

import "os"

func newDefaultSystemManager() (SystemManager, error) {
	if os.Getenv("HOMESTACK_NODE_PROFILE_SOURCE") == "keyring" {
		return &LocalSystemManager{}, nil
	}
	return NewSystemClient(DefaultHelperSocket)
}
