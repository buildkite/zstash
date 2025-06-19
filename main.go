package main

import (
	"context"
	"os"
	"time"

	"github.com/alecthomas/kong"
	kongyaml "github.com/alecthomas/kong-yaml"
	"github.com/buildkite/zstash/internal/api"
	"github.com/buildkite/zstash/internal/commands"
	"github.com/buildkite/zstash/internal/trace"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	version = "dev"

	cli struct {
		Version       kong.VersionFlag
		Debug         bool   `help:"Enable debug mode." default:"false" env:"BUILDKITE_ZSTASH_DEBUG"`
		RegistrySlug  string `flag:"registry-slug" help:"The registry slug to use." env:"BUILDKITE_REGISTRY_SLUG" default:"~"`
		Endpoint      string `flag:"endpoint" help:"The endpoint to use. Defaults to the Buildkite agent API endpoint." default:"https://agent.buildkite.com/v3" env:"BUILDKITE_AGENT_API_ENDPOINT"`
		Token         string `flag:"token" help:"The buildkite agent access token to use." env:"BUILDKITE_AGENT_ACCESS_TOKEN" required:"true"`
		TraceExporter string `flag:"trace-exporter" help:"The trace exporter to use. Defaults to 'noop'." default:"noop" enum:"noop,grpc" env:"BUILDKITE_ZSTASH_TRACE_EXPORTER"`

		Save    commands.SaveCmd    `cmd:"" help:"save files."`
		Restore commands.RestoreCmd `cmd:"" help:"restore files."`
		KeyTest commands.KeyTestCmd `cmd:"" help:"test a key."`
	}
)

func main() {
	ctx := context.Background()

	start := time.Now()

	cmd := kong.Parse(&cli,
		kong.Vars{
			"version": version,
		},
		kong.Configuration(kongyaml.Loader, ".buildkite/cache.yaml"),
		kong.BindTo(ctx, (*context.Context)(nil)))

	tp, err := trace.NewProvider(ctx, cli.TraceExporter, "github.com/buildkite/zstash", version)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create trace provider")
	}
	defer func() {
		_ = tp.Shutdown(ctx)
	}()

	ctx, span := trace.Start(ctx, "zstash")
	defer span.End()

	// create a http client
	client, err := api.NewClient(ctx, version, cli.Endpoint, cli.RegistrySlug, cli.Token)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create API client")
	}

	if cli.Debug {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).Level(zerolog.DebugLevel)
	} else {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).Level(zerolog.InfoLevel)
	}

	err = cmd.Run(&commands.Globals{Debug: cli.Debug, Version: version, Client: client})
	span.RecordError(err)
	cmd.FatalIfErrorf(err)

	log.Info().Str("command", cmd.Command()).Str("duration", time.Since(start).String()).Msg("command completed")
}
