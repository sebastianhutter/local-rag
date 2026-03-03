package gui

import (
	"log/slog"
	"os"
	"os/exec"
)

func (a *App) openLogs() {
	// Ensure the log file exists (e.g. if someone deleted it manually).
	if _, err := os.Stat(a.logPath); os.IsNotExist(err) {
		if f, err := os.Create(a.logPath); err == nil {
			f.Close()
		}
	}

	if err := exec.Command("open", a.logPath).Start(); err != nil {
		slog.Error("failed to open log file", "path", a.logPath, "err", err)
	}
}
