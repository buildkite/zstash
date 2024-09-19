package main

import (
	"context"
	"log"
	"time"

	"github.com/alecthomas/kong"
	"github.com/buildkite/zstash/internal/commands"
)

var (
	version = "dev"

	cli struct {
		Version kong.VersionFlag
		Debug   bool                `help:"Enable debug mode."`
		Save    commands.SaveCmd    `cmd:"" help:"save files."`
		Restore commands.RestoreCmd `cmd:"" help:"restore files."`
	}
)

func main() {

	ctx := context.Background()

	start := time.Now()

	cmd := kong.Parse(&cli,
		kong.Vars{
			"version": version,
		},
		kong.BindTo(ctx, (*context.Context)(nil)))
	err := cmd.Run(&commands.Globals{Debug: cli.Debug})
	cmd.FatalIfErrorf(err)

	log.Println("total time elapsed:", time.Since(start))
}
