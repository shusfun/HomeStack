package main

import (
	"context"
	"os"

	"github.com/wangshangbin/homestack/internal/assetscli"
)

func main() {
	os.Exit(assetscli.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
