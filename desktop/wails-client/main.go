package main

import (
	"ant-chrome/backend"
	"context"
	"embed"
	"log"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

// Vue assets are built by desktop/vue3-ui directly into this directory.
//
//go:embed all:frontend/dist
var assets embed.FS

type App struct{ *backend.App }

func applicationRoot() string {
	cwd, _ := os.Getwd()
	executable, _ := os.Executable()
	candidates := []string{cwd, filepath.Clean(filepath.Join(cwd, "..", "..")), filepath.Dir(executable)}
	for _, candidate := range candidates {
		if _, err := os.Stat(filepath.Join(candidate, "config.yaml")); err == nil {
			absolute, resolveErr := filepath.Abs(candidate)
			if resolveErr == nil {
				return absolute
			}
		}
	}
	return filepath.Dir(executable)
}

func main() {
	root := applicationRoot()
	if err := backend.EnsureRuntimeLayout(root); err != nil {
		log.Printf("prepare runtime layout: %v", err)
	}
	app := &App{App: backend.NewApp(root, "2.0.0")}
	err := wails.Run(&options.App{
		Title:            "Ant Browser Cloud",
		Width:            1440,
		Height:           900,
		MinWidth:         1024,
		MinHeight:        700,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 245, G: 247, B: 250, A: 255},
		OnStartup:        func(ctx context.Context) { backend.Start(app.App, ctx) },
		OnShutdown:       func(ctx context.Context) { backend.Stop(app.App, ctx) },
		OnBeforeClose:    func(ctx context.Context) bool { return backend.ShouldBlockClose(app.App, ctx) },
		Bind:             []interface{}{app},
	})
	if err != nil {
		log.Fatal(err)
	}
}
