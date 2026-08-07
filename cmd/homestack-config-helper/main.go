package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	setupapi "github.com/wangshangbin/homestack/internal/setup"
	"github.com/wangshangbin/homestack/internal/setuphelper"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "migrate-config" {
		if os.Geteuid() != 0 {
			log.Fatal("配置迁移必须由 root 执行")
		}
		migrated, err := setuphelper.NewManager().MigrateControlEnvironment()
		if err != nil {
			log.Fatal(err)
		}
		if migrated {
			log.Print("Control OAuth 配置已迁移为独立 Google/GitHub 字段")
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "switch" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := setuphelper.NewManager().CompleteSwitch(ctx); err != nil {
			log.Fatal(err)
		}
		return
	}
	flags := flag.NewFlagSet("homestack-config-helper", flag.ExitOnError)
	uid := flags.Uint("uid", 0, "允许连接 Helper 的 Control 用户 UID")
	socket := flags.String("socket", setupapi.DefaultSocketPath, "Helper Unix Socket 路径")
	_ = flags.Parse(os.Args[1:])
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := setuphelper.Run(ctx, *socket, uint32(*uid)); err != nil {
		log.Fatal(err)
	}
}
