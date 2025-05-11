package main

import (
	"context"
	"log"
	"time"

	"github.com/alecthomas/kong"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/buildkite/zstash/internal/commands"
	"github.com/buildkite/zstash/internal/trace"
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

	tp, err := trace.NewProvider(ctx, "github.com/buildkite/zstash", version)
	if err != nil {
		log.Fatalf("failed to create trace provider: %v", err)
	}
	defer func() {
		_ = tp.Shutdown(ctx)
	}()

	var span oteltrace.Span
	ctx, span = trace.Start(ctx, "zstash")
	defer span.End()

	start := time.Now()

	cmd := kong.Parse(&cli,
		kong.Vars{
			"version": version,
		},
		kong.BindTo(ctx, (*context.Context)(nil)))
	err = cmd.Run(&commands.Globals{Debug: cli.Debug, Version: version})
	span.RecordError(err)
	cmd.FatalIfErrorf(err)

	log.Printf("command=%s completed duration=%s", cmd.Command(), time.Since(start))
}
