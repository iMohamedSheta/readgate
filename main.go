package main

import (
	"embed"
	"fmt"
	"log"
	"os"

	"readgate/internal/mcpserver"
	"readgate/internal/shotseed"
	"readgate/internal/version"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// MCP stdio mode: `ReadGate mcp` — same single binary, no window,
	// no HTTP. opencode launches it as `type: local`.
	if len(os.Args) > 1 && (os.Args[1] == "mcp" || os.Args[1] == "--mcp") {
		os.Exit(mcpserver.Run())
	}
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v" || os.Args[1] == "version") {
		fmt.Println(version.Version)
		return
	}
	// Screenshot seed: `ReadGate --shot-seed` builds a synthetic demo
	// profile (READGATE_HOME) so release screenshots never leak real data.
	if len(os.Args) > 1 && (os.Args[1] == "--shot-seed" || os.Args[1] == "shot-seed") {
		if err := shotseed.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "shot-seed:", err)
			os.Exit(1)
		}
		fmt.Println("demo profile seeded at " + os.Getenv("READGATE_HOME"))
		return
	}

	app, err := NewApp()
	if err != nil {
		log.Fatalf("readgate init: %v", err)
	}

	err = wails.Run(&options.App{
		Title:     "readgate",
		Width:     1380,
		Height:    900,
		MinWidth:  900,
		MinHeight: 600,
		Frameless: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 9, G: 9, B: 11, A: 1},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}
