package observability

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

func NewLogger(environment string, output io.Writer) *slog.Logger {
	if output == nil {
		output = os.Stdout
	}
	level := slog.LevelInfo
	if strings.EqualFold(environment, "development") {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{Level: level}))
}
