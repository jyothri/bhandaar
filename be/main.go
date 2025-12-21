package main

import (
	"log/slog"
	"os"

	"github.com/jyothri/hdd/db"
	"github.com/jyothri/hdd/web"
)

func init() {
	options := &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				a.Value = slog.StringValue(a.Value.Time().Format("2006-01-02 15:04:05.999"))
			}
			return a
		},
		Level: slog.LevelDebug,
	}

	handler := slog.NewTextHandler(os.Stdout, options)
	logger := slog.New(handler)
	slog.SetDefault(logger)
	slog.SetLogLoggerLevel(slog.LevelDebug)
}

func main() {
	// Initialize database connection
	if err := db.SetupDatabase(); err != nil {
		slog.Error("Failed to initialize database", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("Failed to close database", "error", err)
		}
	}()

	slog.Info("Starting web server")
	web.Server()
}
