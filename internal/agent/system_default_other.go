//go:build !linux

package agent

func newDefaultSystemManager() (SystemManager, error) {
	return &LocalSystemManager{}, nil
}
