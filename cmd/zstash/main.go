package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/alecthomas/kong"
	kongyaml "github.com/alecthomas/kong-yaml"
	"github.com/buildkite/zstash"
	"github.com/buildkite/zstash/api"
	"github.com/buildkite/zstash/cache"
	"github.com/buildkite/zstash/internal/commands"
	"github.com/buildkite/zstash/internal/console"
	"github.com/buildkite/zstash/internal/trace"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	version           = "dev"
	defaultConfigPath = ".buildkite/cache.yml"

	cli struct {
		Version       kong.VersionFlag
		Debug         bool            `help:"Enable debug mode." default:"false" env:"BUILDKITE_ZSTASH_DEBUG"`
		Endpoint      string          `flag:"endpoint" help:"The endpoint to use. Defaults to the Buildkite agent API endpoint." default:"https://agent.buildkite.com/v3" env:"BUILDKITE_AGENT_API_ENDPOINT"`
		Token         string          `flag:"token" help:"The buildkite agent access token to use." env:"BUILDKITE_AGENT_ACCESS_TOKEN" required:"true"`
		TraceExporter string          `flag:"trace-exporter" help:"The trace exporter to use. Defaults to 'noop'." default:"noop" enum:"noop,grpc" env:"BUILDKITE_ZSTASH_TRACE_EXPORTER"`
		Config        kong.ConfigFlag `flag:"config" help:"The path to the cache configuration file. Defaults to .buildkite/cache.yml" default:"${default_config_path}" env:"BUILDKITE_CACHE_CONFIG" `

		commands.CommonFlags

		Caches []cache.Cache // embedded config

		Save    commands.SaveCmd    `cmd:"" help:"save files."`
		Restore commands.RestoreCmd `cmd:"" help:"restore files."`
		KeyTest commands.KeyTestCmd `cmd:"" help:"test a key." hidden:""`
	}
)

func main() {
	ctx := context.Background()

	// Overloads `cli` with configuration file values.
	cmd := kong.Parse(&cli,
		kong.Vars{"version": version, "default_config_path": defaultConfigPath},
		kong.NamedMapper("yamlfile", kongyaml.YAMLFileMapper),
		kong.Configuration(kongyaml.Loader),
		kong.BindTo(ctx, (*context.Context)(nil)))

	err := Run(ctx, cmd)
	cmd.FatalIfErrorf(err)
}

func Run(ctx context.Context, cmd *kong.Context) error {
	start := time.Now()

	// check the token is set
	if cli.Token == "" {
		log.Fatal().Msg("missing token, please set the BUILDKITE_AGENT_ACCESS_TOKEN environment variable")
	}

	tp, err := trace.NewProvider(ctx, cli.TraceExporter, "github.com/buildkite/zstash", version)
	if err != nil {
		return fmt.Errorf("failed to create trace provider: %w", err)
	}
	defer func() {
		_ = tp.Shutdown(ctx)
	}()

	// create a http client
	client := api.NewClient(ctx, version, cli.Endpoint, cli.Token)

	// create cache client
	cacheClient, err := zstash.NewCache(zstash.Config{
		Client:       client,
		BucketURL:    cli.BucketURL,
		Format:       cli.Format,
		Branch:       cli.Branch,
		Pipeline:     cli.Pipeline,
		Organization: cli.Organization,
		Caches:       cli.Caches,
	})
	if err != nil {
		return fmt.Errorf("failed to create cache client: %w", err)
	}

	printer := console.NewPrinter(os.Stderr)

	if cli.Debug {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).Level(zerolog.DebugLevel)
	} else {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).Level(zerolog.ErrorLevel)
	}

	err = cmd.Run(&commands.Globals{Debug: cli.Debug, Version: version, Client: client, CacheClient: cacheClient, Printer: printer, Common: cli.CommonFlags, Caches: cli.Caches})
	if err != nil {
		return fmt.Errorf("command %s failed: %w", cmd.Command(), err)
	}

	printer.Info("✅", "%s completed successfully in %s", cmd.Command(), time.Since(start).String())

	return nil
}
