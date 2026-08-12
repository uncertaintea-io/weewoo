package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"text/tabwriter"

	"github.com/uncertaintea-io/weewoo/internal/config"
	"github.com/uncertaintea-io/weewoo/internal/migrations"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	configfile := flag.String("config", "config.yaml", "Config file")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: migrate [up|status]")
		os.Exit(2)
	}

	// Load the database configuration from the system settings file
	systemSettings, err := config.ReadSystemSettings(*configfile)
	if err != nil {
		slog.Error("Failed to read system settings", slog.Any("error", err))
		os.Exit(1)
	}
	db, err := systemSettings.OpenDatabase()
	if err != nil {
		slog.Error("Failed to create database config", slog.Any("error", err))
		os.Exit(1)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("failed to close database connection", slog.Any("error", err))
		}
	}()

	switch flag.Arg(0) {
	case "up":
		if err := migrations.Apply(context.Background(), db, systemSettings.Database); err != nil {
			slog.Error("failed to apply migrations", slog.Any("error", err))
			os.Exit(1)
		}
		slog.Info("migrations applied")
	case "status":
		statuses, err := migrations.Statuses(context.Background(), db, systemSettings.Database)
		if err != nil {
			slog.Error("failed to list migrations", slog.Any("error", err))
			os.Exit(1)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "VERSION\tNAME\tAPPLIED\tAPPLIED_AT")
		for _, status := range statuses {
			applied := "no"
			appliedAt := "-"
			if status.Applied {
				applied = "yes"
				if status.AppliedAt != nil {
					appliedAt = status.AppliedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
				}
			}

			_, _ = fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", status.Version, status.Name, applied, appliedAt)
		}
		_ = w.Flush()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", flag.Arg(0))
		os.Exit(2)
	}
}
