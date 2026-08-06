package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/wangshangbin/homestack/internal/agent"
	"github.com/wangshangbin/homestack/internal/helper"
)

func main() {
	uid := flag.Uint("uid", 0, "允许连接 helper 的 Agent 用户 UID")
	socket := flag.String("socket", agent.DefaultHelperSocket, "helper Unix Socket 路径")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := helper.Run(ctx, *socket, uint32(*uid)); err != nil {
		log.Fatal(err)
	}
}
