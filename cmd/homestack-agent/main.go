package main

import (
	"fmt"
	"log"
	"os"

	"github.com/wangshangbin/homestack/internal/buildinfo"
	"github.com/wangshangbin/homestack/internal/node"
)

func main() {
	if buildinfo.Requested(os.Args[1:]) {
		fmt.Println(buildinfo.Output("homestack-agent", os.Args[1:]))
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "activate" {
		if err := activate(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "update-helper" {
		if err := runUpdateHelper(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := node.Run(); err != nil {
		log.Fatal(err)
	}
}
