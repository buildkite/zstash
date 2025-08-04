package main

import (
	"context"
	"os"
	"time"

	"github.com/alecthomas/kong"
	kongyaml "github.com/alecthomas/kong-yaml"
	"github.com/buildkite/zstash/internal/api"
	"github.com/buildkite/zstash/internal/commands"
	"github.com/buildkite/zstash/internal/console"
	"github.com/buildkite/zstash/internal/trace"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	version = "dev"

	cli struct {
		Version       kong.VersionFlag
		Debug         bool   `help:"Enable debug mode." default:"false" env:"BUILDKITE_ZSTASH_DEBUG"`
		Endpoint      string `flag:"endpoint" help:"The endpoint to use. Defaults to the Buildkite agent API endpoint." default:"https://agent.buildkite.com/v3" env:"BUILDKITE_AGENT_API_ENDPOINT"`
		Token         string `flag:"token" help:"The buildkite agent access token to use." env:"BUILDKITE_AGENT_ACCESS_TOKEN" required:"true"`
		TraceExporter string `flag:"trace-exporter" help:"The trace exporter to use. Defaults to 'noop'." default:"noop" enum:"noop,grpc" env:"BUILDKITE_ZSTASH_TRACE_EXPORTER"`

		commands.CommonFlags

		Caches []commands.Cache // embedded configuration for caches

		Save    commands.SaveCmd    `cmd:"" help:"save files."`
		Restore commands.RestoreCmd `cmd:"" help:"restore files."`
		KeyTest commands.KeyTestCmd `cmd:"" help:"test a key." hidden:""`
	}
)

func main() {
	ctx := context.Background()

	start := time.Now()

	cmd := kong.Parse(&cli,
		kong.Vars{
			"version": version,
		},
		kong.NamedMapper("yamlfile", kongyaml.YAMLFileMapper),
		kong.Configuration(kongyaml.Loader, ".buildkite/cache.yaml", ".buildkite/cache.yml", ".buildkite/cache.json"),
		kong.BindTo(ctx, (*context.Context)(nil)))

	// check the token is set
	if cli.Token == "" {
		log.Fatal().Msg("missing token, please set the BUILDKITE_AGENT_ACCESS_TOKEN environment variable")
	}

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
	client := api.NewClient(ctx, version, cli.Endpoint, cli.Token)

	printer := console.NewPrinter(os.Stderr)

	if cli.Debug {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).Level(zerolog.DebugLevel)
	} else {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).Level(zerolog.ErrorLevel)
	}

	err = cmd.Run(&commands.Globals{Debug: cli.Debug, Version: version, Client: client, Printer: printer, Caches: cli.Caches, Common: cli.CommonFlags})
	span.RecordError(err)
	cmd.FatalIfErrorf(err)

	printer.Info("✅", "%s completed successfully in %s", cmd.Command(), time.Since(start).String())
}
