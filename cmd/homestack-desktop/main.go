package main

import (
	"fmt"
	"log"
	"os"
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wangshangbin/homestack/assets/brand"
	"github.com/wangshangbin/homestack/internal/buildinfo"
	"github.com/wangshangbin/homestack/internal/desktop"
	"github.com/wangshangbin/homestack/internal/web"
	"github.com/wangshangbin/homestack/internal/windowchrome"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--appimage-update-helper" {
		if err := desktop.RunAppImageUpdateHelper(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
		return
	}
	if buildinfo.Requested(os.Args[1:]) {
		fmt.Println(buildinfo.Output("homestack-desktop", os.Args[1:]))
		return
	}
	service := desktop.NewService()
	app := application.New(application.Options{
		Name:        "HomeStack",
		Description: "多端 NAS 与远程开发整合器",
		Icon:        brand.AppIconPNG(),
		Services: []application.Service{
			application.NewService(service),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(web.Assets()),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})
	if err := service.ConfigureUpdater(app); err != nil {
		log.Printf("桌面更新器不可用: %v", err)
	}
	windowOptions := application.WebviewWindowOptions{
		Title:            "HomeStack",
		Width:            900,
		Height:           680,
		MinWidth:         720,
		MinHeight:        520,
		URL:              "/",
		BackgroundColour: application.NewRGB(245, 247, 248),
	}
	windowchrome.Apply(&windowOptions, runtime.GOOS)
	app.Window.NewWithOptions(windowOptions)
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
