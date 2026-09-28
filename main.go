package main

import (
	"embed"
	"log"
	"os"

	"readgate/internal/mcpserver"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// MCP stdio mode: `ReadGate.exe mcp` — same single binary, no window,
	// no HTTP. opencode launches it as `type: local` (goals parity).
	if len(os.Args) > 1 && (os.Args[1] == "mcp" || os.Args[1] == "--mcp") {
		os.Exit(mcpserver.Run())
	}

	app, err := NewApp()
	if err != nil {
		log.Fatalf("readgate init: %v", err)
	}

	err = wails.Run(&options.App{
		Title:     "ReadGate — AI-safe DB Gateway",
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
