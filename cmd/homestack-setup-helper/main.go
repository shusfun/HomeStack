package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/wangshangbin/homestack/internal/maintenance"
	setupapi "github.com/wangshangbin/homestack/internal/setup"
	"github.com/wangshangbin/homestack/internal/setuphelper"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "switch" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := setuphelper.NewManager().CompleteSwitch(ctx); err != nil {
			log.Fatal(err)
		}
		return
	}
	maintenanceMode := len(os.Args) > 1 && os.Args[1] == "maintenance"
	arguments := os.Args[1:]
	if maintenanceMode {
		arguments = os.Args[2:]
	}
	flags := flag.NewFlagSet("homestack-setup-helper", flag.ExitOnError)
	uid := flags.Uint("uid", 0, "允许连接 Helper 的 Control 用户 UID")
	defaultSocket := setupapi.DefaultSocketPath
	if maintenanceMode {
		defaultSocket = maintenance.DefaultSocketPath
	}
	socket := flags.String("socket", defaultSocket, "Helper Unix Socket 路径")
	_ = flags.Parse(arguments)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var err error
	if maintenanceMode {
		err = setuphelper.RunMaintenance(ctx, *socket, uint32(*uid))
	} else {
		err = setuphelper.Run(ctx, *socket, uint32(*uid))
	}
	if err != nil {
		log.Fatal(err)
	}
}
