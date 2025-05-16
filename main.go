package main

import (
	"context"
	"log"
	"time"

	"github.com/alecthomas/kong"
	kongyaml "github.com/alecthomas/kong-yaml"
	"github.com/buildkite/zstash/internal/api"
	"github.com/buildkite/zstash/internal/commands"
	"github.com/buildkite/zstash/internal/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

var (
	version = "dev"

	cli struct {
		Version      kong.VersionFlag
		Debug        bool   `help:"Enable debug mode."`
		RegistrySlug string `flag:"registry-slug" help:"The registry slug to use." env:"BUILDKITE_REGISTRY_SLUG" default:"~"`
		Endpoint     string `flag:"endpoint" help:"The endpoint to use. Defaults to the Buildkite agent API endpoint." default:"https://agent.buildkite.com/v3"`
		Token        string `flag:"token" help:"The buildkite agent access token to use." env:"BUILDKITE_AGENT_ACCESS_TOKEN" required:"true"`

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
		kong.Configuration(kongyaml.Loader, ".buildkite/cache.yaml"),
		kong.BindTo(ctx, (*context.Context)(nil)))

	// create a http client
	client, err := api.NewClient(ctx, version, cli.Endpoint, cli.RegistrySlug, cli.Token)
	if err != nil {
		log.Fatal(err)
	}

	err = cmd.Run(&commands.Globals{Debug: cli.Debug, Version: version, Client: client})
	span.RecordError(err)
	cmd.FatalIfErrorf(err)

	log.Printf("command=%s completed duration=%s", cmd.Command(), time.Since(start))
}
