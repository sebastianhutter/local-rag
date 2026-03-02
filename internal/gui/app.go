package gui

import (
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/driver/desktop"

	"github.com/sebastianhutter/local-rag-go/internal/config"
	"github.com/sebastianhutter/local-rag-go/internal/parser"
)

// App is the main GUI application.
type App struct {
	fyneApp    fyne.App
	cfg        *config.Config
	cfgMu      sync.RWMutex
	logHandler *RingBufferHandler

	mcpService      MCPService
	indexingService IndexingService
	statusService   StatusService

	// Cached status for menu display.
	statusMu    sync.Mutex
	statusLabel string

	// mcpStarting is true while the auto-start goroutine is in flight.
	mcpStarting bool

	// Window references (nil when not open).
	settingsWin fyne.Window
	logsWin     fyne.Window

	// rebuildCh coalesces menu rebuild requests from any goroutine into a
	// single goroutine that actually calls SetSystemTrayMenu. This avoids
	// the concurrent-close panic inside fyne.io/systray.ResetMenu.
	rebuildCh chan struct{}

	// lastReindex tracks the last time a full reindex completed so both the
	// periodic timer and the wake handler respect the configured interval.
	lastReindexMu sync.Mutex
	lastReindex   time.Time

	// Shutdown signal.
	done chan struct{}
}

// Run is the main entry point for the GUI, called by the "gui" cobra command.
func Run() error {
	// Load config.
	cfg, err := config.Load("")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Install ring-buffer slog handler.
	handler := NewRingBufferHandler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(slog.New(handler))

	a := &App{
		cfg:         cfg,
		logHandler:  handler,
		statusLabel: "Loading...",
		rebuildCh:   make(chan struct{}, 1),
		done:        make(chan struct{}),
	}

	// Create Fyne app — this must be called on the main goroutine.
	a.fyneApp = app.New()
	a.fyneApp.SetIcon(appIcon())

	// Build initial system tray menu (safe — no concurrent callers yet).
	a.doRebuildMenu()

	// Start the single menu-rebuild goroutine. All future rebuilds go
	// through requestRebuild → rebuildCh → this goroutine.
	go a.menuRebuildLoop()

	// Start background timers.
	go a.statusTimer()
	go a.autoReindexTimer()

	// Auto-start MCP if configured.
	if cfg.GUI.AutoStartMCP {
		a.mcpStarting = true
		go func() {
			if err := a.mcpService.Start(cfg.GUI.MCPPort); err != nil {
				slog.Error("auto-start MCP failed", "err", err)
			}
			a.mcpStarting = false
			a.requestRebuild()
		}()
	}

	// Initial status update.
	go a.updateStatus()

	slog.Info("local-rag GUI started")

	// Run the Fyne event loop (blocks until quit).
	a.fyneApp.Run()

	// Cleanup.
	close(a.done)
	a.mcpService.Stop()
	parser.ClosePDFPool()
	slog.Info("local-rag GUI stopped")

	return nil
}

// requestRebuild asks the menu-rebuild goroutine to refresh the tray menu.
// Multiple rapid calls are coalesced into one rebuild.
func (a *App) requestRebuild() {
	select {
	case a.rebuildCh <- struct{}{}:
	default:
		// A rebuild is already pending — skip.
	}
}

// menuRebuildLoop drains rebuildCh and calls doRebuildMenu serially. It also
// adds a small debounce (50ms) so that bursts of requests collapse into one.
func (a *App) menuRebuildLoop() {
	for {
		select {
		case <-a.rebuildCh:
			// Debounce: wait briefly for more requests to coalesce.
			time.Sleep(50 * time.Millisecond)
			// Drain any extra signals that arrived during the debounce.
			for {
				select {
				case <-a.rebuildCh:
				default:
					goto drained
				}
			}
		drained:
			fyne.Do(func() {
				a.doRebuildMenu()
			})

		case <-a.done:
			return
		}
	}
}

// doRebuildMenu builds and sets the system tray menu. Must be called from
// a single goroutine (menuRebuildLoop) or before concurrency starts.
func (a *App) doRebuildMenu() {
	a.cfgMu.RLock()
	cfg := a.cfg
	a.cfgMu.RUnlock()

	menu := fyne.NewMenu("local-rag",
		a.mcpMenuItem(),
		a.statusMenuItem(),
	)

	// Index submenu.
	indexItems := []*fyne.MenuItem{
		{Label: "All Collections", Action: func() { a.triggerIndex("") }},
		fyne.NewMenuItemSeparator(),
	}

	// System collections.
	for _, name := range []string{"Obsidian", "Email", "Calibre", "RSS"} {
		n := name
		indexItems = append(indexItems, &fyne.MenuItem{
			Label:  n,
			Action: func() { a.triggerIndex(nameToCollectionKey(n)) },
		})
	}

	// Dynamic code groups from config.
	if len(cfg.CodeGroups) > 0 {
		indexItems = append(indexItems, fyne.NewMenuItemSeparator())
		for groupName := range cfg.CodeGroups {
			gn := groupName
			indexItems = append(indexItems, &fyne.MenuItem{
				Label:  gn,
				Action: func() { a.triggerIndex(gn) },
			})
		}
	}

	indexMenu := &fyne.MenuItem{
		Label:     "Index",
		ChildMenu: fyne.NewMenu("", indexItems...),
	}

	// Disable index menu while indexing.
	if a.indexingService.IsRunning() {
		indexMenu.Disabled = true
	}

	menu.Items = append(menu.Items, indexMenu)
	menu.Items = append(menu.Items,
		&fyne.MenuItem{Label: "Settings...", Action: a.openSettings},
		&fyne.MenuItem{Label: "View Logs...", Action: a.openLogs},
		fyne.NewMenuItemSeparator(),
		&fyne.MenuItem{Label: "Quit", Action: func() { a.fyneApp.Quit() }},
	)

	if desk, ok := a.fyneApp.(desktop.App); ok {
		desk.SetSystemTrayMenu(menu)
	}
}

func (a *App) mcpMenuItem() *fyne.MenuItem {
	var label string
	switch {
	case a.mcpService.IsRunning():
		label = fmt.Sprintf("MCP: Running (:%d)", a.mcpService.Port())
	case a.mcpStarting:
		label = "MCP: Starting..."
	default:
		label = "MCP: Stopped"
	}
	return &fyne.MenuItem{
		Label:    label,
		Action:   func() { a.toggleMCP() },
		Disabled: a.mcpStarting,
	}
}

func (a *App) statusMenuItem() *fyne.MenuItem {
	a.statusMu.Lock()
	label := a.statusLabel
	a.statusMu.Unlock()

	if a.indexingService.IsRunning() {
		label = fmt.Sprintf("Indexing: %s...", a.indexingService.CurrentLabel())
	}
	return &fyne.MenuItem{
		Label:    label,
		Disabled: true,
	}
}

func (a *App) toggleMCP() {
	if a.mcpService.IsRunning() {
		a.mcpService.Stop()
	} else {
		a.cfgMu.RLock()
		port := a.cfg.GUI.MCPPort
		a.cfgMu.RUnlock()

		if err := a.mcpService.Start(port); err != nil {
			slog.Error("failed to start MCP", "err", err)
			a.fyneApp.SendNotification(fyne.NewNotification("local-rag", "Failed to start MCP: "+err.Error()))
		}
	}
	a.requestRebuild()
}

func (a *App) triggerIndex(collection string) {
	if a.indexingService.IsRunning() {
		return
	}

	a.cfgMu.RLock()
	c := a.cfg
	a.cfgMu.RUnlock()

	onComplete := func(err error) {
		msg := "Indexing completed"
		if err != nil {
			msg = "Indexing error: " + err.Error()
		}
		fyne.Do(func() {
			a.fyneApp.SendNotification(fyne.NewNotification("local-rag", msg))
		})
		a.updateStatus()
	}

	if collection == "" {
		go a.indexingService.IndexAll(c, onComplete)
	} else {
		go a.indexingService.IndexCollection(collection, c, onComplete)
	}
	// Rebuild after a short delay to show "Indexing: ..." status.
	go func() {
		time.Sleep(200 * time.Millisecond)
		a.requestRebuild()
	}()
}

func (a *App) updateStatus() {
	a.cfgMu.RLock()
	c := a.cfg
	a.cfgMu.RUnlock()

	ov := a.statusService.GetOverview(c)

	a.statusMu.Lock()
	a.statusLabel = fmt.Sprintf("%d collections, %d chunks", ov.CollectionCount, ov.ChunkCount)
	a.statusMu.Unlock()

	a.requestRebuild()
}

// ---------------------------------------------------------------------------
// Timers
// ---------------------------------------------------------------------------

func (a *App) statusTimer() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			a.updateStatus()
		case <-a.done:
			return
		}
	}
}

func (a *App) autoReindexTimer() {
	// Check every 60 seconds whether a reindex is due. This lets the timer
	// pick up config changes (enable/disable, interval) without a restart.
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			a.tryAutoReindex("timer")
		case <-a.done:
			return
		}
	}
}

// tryAutoReindex triggers a full reindex only if auto-reindex is enabled, the
// configured interval has elapsed since the last reindex, and no indexing is
// already running. Called by both the wake handler and the periodic timer.
func (a *App) tryAutoReindex(reason string) {
	a.cfgMu.RLock()
	autoReindex := a.cfg.GUI.AutoReindex
	minutes := a.cfg.GUI.AutoReindexIntervalMinutes
	c := a.cfg
	a.cfgMu.RUnlock()

	if !autoReindex || minutes <= 0 {
		return
	}
	if a.indexingService.IsRunning() {
		return
	}

	interval := time.Duration(minutes) * time.Minute

	a.lastReindexMu.Lock()
	if time.Since(a.lastReindex) < interval {
		a.lastReindexMu.Unlock()
		slog.Debug("skipping auto-reindex, interval not reached", "reason", reason)
		return
	}
	a.lastReindex = time.Now()
	a.lastReindexMu.Unlock()

	slog.Info("auto-reindex triggered", "reason", reason, "interval", interval)
	go func() {
		a.indexingService.IndexAll(c, func(err error) {
			if err == nil {
				fyne.Do(func() {
					a.fyneApp.SendNotification(fyne.NewNotification("local-rag", "Re-index completed ("+reason+")"))
				})
			}
			a.requestRebuild()
		})
		a.requestRebuild()
	}()
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func nameToCollectionKey(display string) string {
	switch display {
	case "Obsidian":
		return "obsidian"
	case "Email":
		return "email"
	case "Calibre":
		return "calibre"
	case "RSS":
		return "rss"
	default:
		return display
	}
}

// ReloadConfig re-reads config.json and updates the app's config reference.
func (a *App) ReloadConfig() {
	cfg, err := config.Load("")
	if err != nil {
		slog.Error("reload config failed", "err", err)
		return
	}
	a.cfgMu.Lock()
	a.cfg = cfg
	a.cfgMu.Unlock()
	a.requestRebuild()
}
