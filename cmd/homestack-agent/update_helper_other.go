//go:build !linux

package main

import "errors"

func runUpdateHelper([]string) error {
	return errors.New("Agent 更新 helper 只支持 Linux")
}
