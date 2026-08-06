package main

import (
	"fmt"
	"log"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wangshangbin/homestack/internal/buildinfo"
	"github.com/wangshangbin/homestack/internal/desktop"
	"github.com/wangshangbin/homestack/internal/web"
)

func main() {
	if buildinfo.Requested(os.Args[1:]) {
		fmt.Println(buildinfo.String("homestack-desktop"))
		return
	}
	service := desktop.NewService()
	app := application.New(application.Options{
		Name:        "HomeStack",
		Description: "多端 NAS 与远程开发整合器",
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
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "HomeStack",
		Width:            1180,
		Height:           760,
		MinWidth:         860,
		MinHeight:        600,
		URL:              "/",
		BackgroundColour: application.NewRGB(245, 247, 248),
	})
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
