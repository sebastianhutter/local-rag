package gui

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"howett.net/plist"
)

const (
	launchdLabel = "com.local-rag.gui"
	plistName    = launchdLabel + ".plist"
)

func plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", plistName)
}

// SetStartOnLogin writes or removes the launchd plist for auto-start on login.
func SetStartOnLogin(enabled bool) error {
	path := plistPath()

	if !enabled {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove plist: %w", err)
		}
		slog.Info("removed start-on-login plist", "path", path)
		return nil
	}

	// Find our own binary path.
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("resolve symlinks: %w", err)
	}

	plistData := map[string]any{
		"Label":            launchdLabel,
		"ProgramArguments": []string{exe, "gui"},
		"RunAtLoad":        true,
		"KeepAlive":        false,
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create LaunchAgents dir: %w", err)
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create plist: %w", err)
	}
	defer f.Close()

	enc := plist.NewEncoder(f)
	enc.Indent("\t")
	if err := enc.Encode(plistData); err != nil {
		return fmt.Errorf("encode plist: %w", err)
	}

	slog.Info("wrote start-on-login plist", "path", path)
	return nil
}

// IsStartOnLoginInstalled checks if the launchd plist exists.
func IsStartOnLoginInstalled() bool {
	_, err := os.Stat(plistPath())
	return err == nil
}
