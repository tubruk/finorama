package logx

import (
	"log/slog"
	"os"
	"time"

	"github.com/lmittmann/tint"
	"github.com/mattn/go-isatty"
)

// InitLogger configures the global slog logger.
// If stderr is a terminal (TTY), it enables ANSI colored output.
// If running in Docker, systemd, or redirected non-TTY, color is automatically disabled.
func InitLogger() {
	isTTY := isatty.IsTerminal(os.Stderr.Fd()) || isatty.IsCygwinTerminal(os.Stderr.Fd())

	w := os.Stderr
	logger := slog.New(tint.NewHandler(w, &tint.Options{
		Level:      slog.LevelInfo,
		TimeFormat: time.TimeOnly,
		NoColor:    !isTTY,
	}))

	slog.SetDefault(logger)
}
