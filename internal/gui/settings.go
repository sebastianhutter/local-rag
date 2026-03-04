package gui

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/sebastianhutter/local-rag-go/internal/config"
)

// minHeightLayout forces a minimum height on its single child.
type minHeightLayout struct {
	minH float32
}

func (l *minHeightLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	w := float32(0)
	for _, o := range objects {
		if s := o.MinSize(); s.Width > w {
			w = s.Width
		}
	}
	return fyne.NewSize(w, l.minH)
}

func (l *minHeightLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objects {
		o.Resize(size)
		o.Move(fyne.NewPos(0, 0))
	}
}

// withMinHeight wraps a canvas object so it has at least the given height.
func withMinHeight(obj fyne.CanvasObject, h float32) *fyne.Container {
	return container.New(&minHeightLayout{minH: h}, obj)
}

var _ fyne.Layout = (*minHeightLayout)(nil)

func (a *App) openSettings() {
	// Raise existing window if already open.
	if a.settingsWin != nil {
		a.settingsWin.Show()
		a.settingsWin.RequestFocus()
		return
	}

	w := a.fyneApp.NewWindow("local-rag Settings")
	w.Resize(fyne.NewSize(700, 500))
	a.settingsWin = w

	// Work on a copy of the config so cancel discards changes.
	cfg := *a.cfg

	// Build all 7 tabs.
	generalTab := a.buildGeneralTab(&cfg, w)
	sourcesTab := a.buildSourcesTab(&cfg, w)
	codeGroupsTab := a.buildCodeGroupsTab(&cfg, w)
	searchTab := a.buildSearchTab(&cfg)
	ocrTab := a.buildOCRTab(&cfg)
	mcpTab := a.buildMCPTab(&cfg, w)
	collectionsTab := a.buildCollectionsTab(&cfg, w)

	tabs := container.NewAppTabs(
		container.NewTabItem("General", generalTab),
		container.NewTabItem("Sources", sourcesTab),
		container.NewTabItem("Code Groups", codeGroupsTab),
		container.NewTabItem("Search", searchTab),
		container.NewTabItem("OCR", ocrTab),
		container.NewTabItem("MCP & Scheduling", mcpTab),
		container.NewTabItem("Collections", collectionsTab),
	)

	saveBtn := widget.NewButton("Save", func() {
		if err := config.Save(&cfg, ""); err != nil {
			slog.Error("save config failed", "err", err)
			dialog.ShowError(err, w)
			return
		}
		// Sync launchd plist with start-on-login setting.
		if err := SetStartOnLogin(cfg.GUI.StartOnLogin); err != nil {
			slog.Error("set start-on-login failed", "err", err)
		}
		a.ReloadConfig()
		dialog.ShowInformation("Settings Saved",
			"Some changes require a restart to take effect.\nPlease quit and relaunch local-rag.",
			w)
	})

	cancelBtn := widget.NewButton("Cancel", func() {
		w.Close()
	})

	buttons := container.NewHBox(saveBtn, cancelBtn)
	content := container.NewBorder(nil, buttons, nil, nil, tabs)

	w.SetContent(content)
	w.SetOnClosed(func() {
		a.settingsWin = nil
	})
	w.Show()
}

// ---------------------------------------------------------------------------
// Tab 1 — General
// ---------------------------------------------------------------------------

func (a *App) buildGeneralTab(cfg *config.Config, w fyne.Window) fyne.CanvasObject {
	dbPathEntry := widget.NewEntry()
	dbPathEntry.SetText(cfg.DBPath)
	dbPathEntry.OnChanged = func(s string) { cfg.DBPath = s }

	dbBrowseBtn := widget.NewButton("Browse", func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if uri != nil {
				dbPathEntry.SetText(uri.Path())
			}
		}, w)
	})

	modelSelect := widget.NewSelect(
		[]string{"bge-m3", "mxbai-embed-large", "nomic-embed-text"},
		func(s string) { cfg.EmbeddingModel = s },
	)
	modelSelect.SetSelected(cfg.EmbeddingModel)

	dimEntry := widget.NewEntry()
	dimEntry.SetText(strconv.Itoa(cfg.EmbeddingDimensions))
	dimEntry.Validator = intValidator(1, 8192)
	dimEntry.OnChanged = func(s string) {
		if v, err := strconv.Atoi(s); err == nil {
			cfg.EmbeddingDimensions = v
		}
	}

	chunkEntry := widget.NewEntry()
	chunkEntry.SetText(strconv.Itoa(cfg.ChunkSizeTokens))
	chunkEntry.Validator = intValidator(50, 10000)
	chunkEntry.OnChanged = func(s string) {
		if v, err := strconv.Atoi(s); err == nil {
			cfg.ChunkSizeTokens = v
		}
	}

	overlapEntry := widget.NewEntry()
	overlapEntry.SetText(strconv.Itoa(cfg.ChunkOverlapTokens))
	overlapEntry.Validator = intValidator(0, 1000)
	overlapEntry.OnChanged = func(s string) {
		if v, err := strconv.Atoi(s); err == nil {
			cfg.ChunkOverlapTokens = v
		}
	}

	form := widget.NewForm(
		widget.NewFormItem("Database path", container.NewBorder(nil, nil, nil, dbBrowseBtn, dbPathEntry)),
		widget.NewFormItem("Embedding model", modelSelect),
		widget.NewFormItem("Embedding dimensions", dimEntry),
		widget.NewFormItem("Chunk size (tokens)", chunkEntry),
		widget.NewFormItem("Chunk overlap (tokens)", overlapEntry),
	)

	return container.NewVScroll(form)
}

// ---------------------------------------------------------------------------
// Tab 2 — Sources
// ---------------------------------------------------------------------------

func (a *App) buildSourcesTab(cfg *config.Config, w fyne.Window) fyne.CanvasObject {
	// Obsidian vaults
	vaultsList := newStringListWidget(&cfg.ObsidianVaults, "Add Vault", w, true)

	// Exclude folders
	excludeList := newStringListWidget(&cfg.ObsidianExcludeFolders, "Add Folder", w, false)

	// eM Client path
	emEntry := widget.NewEntry()
	emEntry.SetText(cfg.EmclientDBPath)
	emEntry.OnChanged = func(s string) { cfg.EmclientDBPath = s }
	emBrowse := widget.NewButton("Browse", func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if uri != nil {
				emEntry.SetText(uri.Path())
			}
		}, w)
	})

	// Calibre libraries
	calibreList := newStringListWidget(&cfg.CalibreLibraries, "Add Library", w, true)

	// NetNewsWire path
	nnwEntry := widget.NewEntry()
	nnwEntry.SetText(cfg.NetnewswireDBPath)
	nnwEntry.OnChanged = func(s string) { cfg.NetnewswireDBPath = s }
	nnwBrowse := widget.NewButton("Browse", func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if uri != nil {
				nnwEntry.SetText(uri.Path())
			}
		}, w)
	})

	return container.NewVScroll(container.NewVBox(
		widget.NewCard("Obsidian Vaults", "", vaultsList),
		widget.NewCard("Exclude Folders", "", excludeList),
		widget.NewCard("eM Client", "", container.NewBorder(nil, nil, nil, emBrowse, emEntry)),
		widget.NewCard("Calibre Libraries", "", calibreList),
		widget.NewCard("NetNewsWire", "", container.NewBorder(nil, nil, nil, nnwBrowse, nnwEntry)),
	))
}

// ---------------------------------------------------------------------------
// Tab 3 — Code Groups
// ---------------------------------------------------------------------------

func (a *App) buildCodeGroupsTab(cfg *config.Config, w fyne.Window) fyne.CanvasObject {
	if cfg.CodeGroups == nil {
		cfg.CodeGroups = make(map[string][]string)
	}

	// Flat VBox that renders groups + repos as plain labels.
	// Rebuilt whenever groups change via the rebuild() closure.
	groupsBox := container.NewVBox()

	var rebuild func()
	rebuild = func() {
		groupsBox.RemoveAll()
		if len(cfg.CodeGroups) == 0 {
			groupsBox.Add(widget.NewLabel("No code groups configured."))
			return
		}
		for groupName, repos := range cfg.CodeGroups {
			gn := groupName
			header := widget.NewLabel(gn)
			header.TextStyle.Bold = true

			removeGroupBtn := widget.NewButton("Remove Group", func() {
				dialog.ShowConfirm("Remove Group",
					fmt.Sprintf("Remove group '%s' and all its repos?", gn),
					func(yes bool) {
						if yes {
							delete(cfg.CodeGroups, gn)
							rebuild()
						}
					}, w)
			})

			groupsBox.Add(container.NewHBox(header, removeGroupBtn))
			for _, repo := range repos {
				repoLabel := widget.NewLabel("    " + repo)
				repoLabel.Wrapping = fyne.TextTruncate
				groupsBox.Add(repoLabel)
			}
			groupsBox.Add(widget.NewSeparator())
		}
	}
	rebuild()

	addGroupBtn := widget.NewButton("Add Group", func() {
		entry := widget.NewEntry()
		entry.SetPlaceHolder("Group name")
		dialog.ShowForm("Add Code Group", "Add", "Cancel",
			[]*widget.FormItem{widget.NewFormItem("Name", entry)},
			func(ok bool) {
				if ok && entry.Text != "" {
					cfg.CodeGroups[entry.Text] = []string{}
					rebuild()
				}
			}, w)
	})

	addRepoBtn := widget.NewButton("Add Repo", func() {
		if len(cfg.CodeGroups) == 0 {
			dialog.ShowInformation("No Groups", "Create a code group first.", w)
			return
		}
		var names []string
		for name := range cfg.CodeGroups {
			names = append(names, name)
		}
		sel := widget.NewSelect(names, nil)
		sel.SetSelected(names[0])
		dialog.ShowForm("Select Group", "Next", "Cancel",
			[]*widget.FormItem{widget.NewFormItem("Group", sel)},
			func(ok bool) {
				if !ok || sel.Selected == "" {
					return
				}
				group := sel.Selected
				dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
					if uri != nil {
						cfg.CodeGroups[group] = append(cfg.CodeGroups[group], uri.Path())
						rebuild()
					}
				}, w)
			}, w)
	})

	// Git history months
	historyEntry := widget.NewEntry()
	historyEntry.SetText(strconv.Itoa(cfg.GitHistoryInMonths))
	historyEntry.Validator = intValidator(0, 120)
	historyEntry.OnChanged = func(s string) {
		if v, err := strconv.Atoi(s); err == nil {
			cfg.GitHistoryInMonths = v
		}
	}

	// Commit blacklist
	blacklistWidget := newStringListWidget(&cfg.GitCommitSubjectBlacklist, "Add Pattern", w, false)

	buttons := container.NewHBox(addGroupBtn, addRepoBtn)

	return container.NewVScroll(container.NewVBox(
		widget.NewCard("Code Groups", "", container.NewVBox(buttons, groupsBox)),
		widget.NewCard("Git History", "",
			widget.NewForm(
				widget.NewFormItem("History months", historyEntry),
			)),
		widget.NewCard("Commit Subject Blacklist", "", blacklistWidget),
	))
}

// ---------------------------------------------------------------------------
// Tab 4 — Search
// ---------------------------------------------------------------------------

func (a *App) buildSearchTab(cfg *config.Config) fyne.CanvasObject {
	topKEntry := widget.NewEntry()
	topKEntry.SetText(strconv.Itoa(cfg.SearchDefaults.TopK))
	topKEntry.Validator = intValidator(1, 1000)
	topKEntry.OnChanged = func(s string) {
		if v, err := strconv.Atoi(s); err == nil {
			cfg.SearchDefaults.TopK = v
		}
	}

	rrfKEntry := widget.NewEntry()
	rrfKEntry.SetText(strconv.Itoa(cfg.SearchDefaults.RRFK))
	rrfKEntry.Validator = intValidator(1, 1000)
	rrfKEntry.OnChanged = func(s string) {
		if v, err := strconv.Atoi(s); err == nil {
			cfg.SearchDefaults.RRFK = v
		}
	}

	vecEntry := widget.NewEntry()
	vecEntry.SetText(fmt.Sprintf("%.2f", cfg.SearchDefaults.VectorWeight))
	vecEntry.OnChanged = func(s string) {
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			cfg.SearchDefaults.VectorWeight = v
		}
	}

	ftsEntry := widget.NewEntry()
	ftsEntry.SetText(fmt.Sprintf("%.2f", cfg.SearchDefaults.FTSWeight))
	ftsEntry.OnChanged = func(s string) {
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			cfg.SearchDefaults.FTSWeight = v
		}
	}

	form := widget.NewForm(
		widget.NewFormItem("Top K results", topKEntry),
		widget.NewFormItem("RRF K parameter", rrfKEntry),
		widget.NewFormItem("Vector weight", vecEntry),
		widget.NewFormItem("FTS weight", ftsEntry),
	)

	return container.NewVScroll(form)
}

// ---------------------------------------------------------------------------
// Tab 5 — OCR
// ---------------------------------------------------------------------------

func (a *App) buildOCRTab(cfg *config.Config) fyne.CanvasObject {
	enabledCheck := widget.NewCheck("Enable OCR fallback for scanned PDFs", func(b bool) {
		cfg.OCR.Enabled = b
	})
	enabledCheck.Checked = cfg.OCR.Enabled

	langEntry := widget.NewEntry()
	langEntry.SetText(strings.Join(cfg.OCR.Languages, ", "))
	langEntry.SetPlaceHolder("eng, deu")
	langEntry.OnChanged = func(s string) {
		var langs []string
		for _, l := range strings.Split(s, ",") {
			l = strings.TrimSpace(l)
			if l != "" {
				langs = append(langs, l)
			}
		}
		if len(langs) == 0 {
			langs = []string{"eng"}
		}
		cfg.OCR.Languages = langs
	}

	maxPagesEntry := widget.NewEntry()
	maxPagesEntry.SetText(strconv.Itoa(cfg.OCR.MaxPages))
	maxPagesEntry.Validator = intValidator(1, 10000)
	maxPagesEntry.OnChanged = func(s string) {
		if v, err := strconv.Atoi(s); err == nil {
			cfg.OCR.MaxPages = v
		}
	}

	maxSizeEntry := widget.NewEntry()
	maxSizeEntry.SetText(strconv.Itoa(cfg.OCR.MaxFileSizeMB))
	maxSizeEntry.Validator = intValidator(1, 10000)
	maxSizeEntry.OnChanged = func(s string) {
		if v, err := strconv.Atoi(s); err == nil {
			cfg.OCR.MaxFileSizeMB = v
		}
	}

	minWordsEntry := widget.NewEntry()
	minWordsEntry.SetText(strconv.Itoa(cfg.OCR.MinWordCount))
	minWordsEntry.Validator = intValidator(0, 1000)
	minWordsEntry.OnChanged = func(s string) {
		if v, err := strconv.Atoi(s); err == nil {
			cfg.OCR.MinWordCount = v
		}
	}

	form := widget.NewForm(
		widget.NewFormItem("Languages", langEntry),
		widget.NewFormItem("Max pages", maxPagesEntry),
		widget.NewFormItem("Max file size (MB)", maxSizeEntry),
		widget.NewFormItem("Min word count", minWordsEntry),
	)

	helpLabel := widget.NewLabel("Requires: brew install tesseract\nPages with fewer words than the threshold are rendered and OCR'd via tesseract.")
	helpLabel.Wrapping = fyne.TextWrapWord

	return container.NewVScroll(container.NewVBox(
		enabledCheck,
		form,
		widget.NewSeparator(),
		helpLabel,
	))
}

// ---------------------------------------------------------------------------
// Tab 6 — MCP & Scheduling
// ---------------------------------------------------------------------------

func (a *App) buildMCPTab(cfg *config.Config, w fyne.Window) fyne.CanvasObject {
	autoStartCheck := widget.NewCheck("Auto-start MCP server", func(b bool) {
		cfg.GUI.AutoStartMCP = b
	})
	autoStartCheck.Checked = cfg.GUI.AutoStartMCP

	portEntry := widget.NewEntry()
	portEntry.SetText(strconv.Itoa(cfg.GUI.MCPPort))
	portEntry.Validator = intValidator(1024, 65535)
	portEntry.OnChanged = func(s string) {
		if v, err := strconv.Atoi(s); err == nil {
			cfg.GUI.MCPPort = v
		}
	}

	// Registration help text.
	regHelp := widget.NewMultiLineEntry()
	regHelp.Disable()
	regHelp.SetText(fmt.Sprintf(`Claude Code (.mcp.json):
{
  "mcpServers": {
    "local-rag": {
      "type": "sse",
      "url": "http://127.0.0.1:%d/sse"
    }
  }
}

Claude Desktop (claude_desktop_config.json):
{
  "mcpServers": {
    "local-rag": {
      "type": "sse",
      "url": "http://127.0.0.1:%d/sse"
    }
  }
}`, cfg.GUI.MCPPort, cfg.GUI.MCPPort))

	autoReindexCheck := widget.NewCheck("Auto-reindex periodically", func(b bool) {
		cfg.GUI.AutoReindex = b
	})
	autoReindexCheck.Checked = cfg.GUI.AutoReindex

	intervalEntry := widget.NewEntry()
	intervalEntry.SetText(strconv.Itoa(cfg.GUI.AutoReindexIntervalMinutes))
	intervalEntry.Validator = intValidator(1, 10080) // up to 7 days in minutes
	intervalEntry.OnChanged = func(s string) {
		if v, err := strconv.Atoi(s); err == nil {
			cfg.GUI.AutoReindexIntervalMinutes = v
		}
	}

	startOnLoginCheck := widget.NewCheck("Start on login", func(b bool) {
		cfg.GUI.StartOnLogin = b
	})
	startOnLoginCheck.Checked = cfg.GUI.StartOnLogin

	return container.NewVScroll(container.NewVBox(
		widget.NewCard("MCP Server", "", container.NewVBox(
			autoStartCheck,
			widget.NewForm(widget.NewFormItem("Port", portEntry)),
		)),
		widget.NewCard("Registration", "", regHelp),
		widget.NewCard("Scheduling", "", container.NewVBox(
			autoReindexCheck,
			widget.NewForm(widget.NewFormItem("Interval (minutes)", intervalEntry)),
			startOnLoginCheck,
		)),
	))
}

// ---------------------------------------------------------------------------
// Tab 6 — Collections
// ---------------------------------------------------------------------------

func (a *App) buildCollectionsTab(cfg *config.Config, w fyne.Window) fyne.CanvasObject {
	// Summary label — start with placeholder, load in background.
	summaryLabel := widget.NewLabel("Loading...")

	// Collection data — start empty, load in background.
	var collections []CollectionInfo

	headers := []string{"Enabled", "Name", "Type", "Chunks", "Last Indexed"}

	table := widget.NewTable(
		// length
		func() (int, int) {
			return len(collections) + 1, len(headers) // +1 for header row
		},
		// create
		func() fyne.CanvasObject {
			return widget.NewLabel("template text here")
		},
		// update
		func(id widget.TableCellID, o fyne.CanvasObject) {
			label := o.(*widget.Label)
			if id.Row == 0 {
				label.SetText(headers[id.Col])
				label.TextStyle.Bold = true
				return
			}
			ci := collections[id.Row-1]
			switch id.Col {
			case 0:
				if ci.Enabled {
					label.SetText("Yes")
				} else {
					label.SetText("No")
				}
			case 1:
				label.SetText(ci.Name)
			case 2:
				label.SetText(ci.Type)
			case 3:
				label.SetText(strconv.Itoa(ci.ChunkCount))
			case 4:
				if ci.LastIndexed != "" {
					label.SetText(ci.LastIndexed)
				} else {
					label.SetText("never")
				}
			}
		},
	)

	// Set column widths.
	table.SetColumnWidth(0, 70)
	table.SetColumnWidth(1, 150)
	table.SetColumnWidth(2, 80)
	table.SetColumnWidth(3, 80)
	table.SetColumnWidth(4, 200)

	refreshData := func() {
		go func() {
			cols := a.statusService.GetCollections(cfg)
			if cols == nil {
				cols = []CollectionInfo{}
			}
			ov := a.statusService.GetOverview(cfg)
			ollamaStatus := "Offline"
			if a.statusService.CheckOllama() {
				ollamaStatus = "OK"
			}
			fyne.Do(func() {
				collections = cols
				summaryLabel.SetText(fmt.Sprintf(
					"DB: %.1f MB | %d collections | %d chunks | Ollama: %s",
					ov.DBSizeMB, ov.CollectionCount, ov.ChunkCount, ollamaStatus,
				))
				table.Refresh()
			})
		}()
	}

	refreshBtn := widget.NewButton("Refresh", func() {
		refreshData()
	})

	deleteBtn := widget.NewButton("Delete Collection...", func() {
		if len(collections) == 0 {
			return
		}
		var names []string
		for _, c := range collections {
			names = append(names, c.Name)
		}
		sel := widget.NewSelect(names, nil)
		sel.SetSelected(names[0])
		dialog.ShowForm("Delete Collection", "Delete", "Cancel",
			[]*widget.FormItem{widget.NewFormItem("Collection", sel)},
			func(ok bool) {
				if !ok || sel.Selected == "" {
					return
				}
				dialog.ShowConfirm("Confirm Delete",
					fmt.Sprintf("Delete collection '%s' and all its data?", sel.Selected),
					func(yes bool) {
						if !yes {
							return
						}
						a.deleteCollection(sel.Selected)
						// Refresh.
						collections = a.statusService.GetCollections(cfg)
						if collections == nil {
							collections = []CollectionInfo{}
						}
						table.Refresh()
					}, w)
			}, w)
	})

	buttons := container.NewHBox(refreshBtn, deleteBtn)

	// Load data asynchronously so the settings window opens instantly.
	refreshData()

	return container.NewBorder(
		container.NewVBox(summaryLabel, buttons), nil, nil, nil,
		table,
	)
}

func (a *App) deleteCollection(name string) {
	conn, err := openDB(a.cfg)
	if err != nil {
		slog.Error("delete collection: open DB failed", "err", err)
		return
	}
	defer conn.Close()

	var id int64
	err = conn.QueryRow("SELECT id FROM collections WHERE name = ?", name).Scan(&id)
	if err != nil {
		slog.Error("delete collection: not found", "name", name)
		return
	}

	conn.Exec("DELETE FROM vec_documents WHERE document_id IN (SELECT id FROM documents WHERE collection_id = ?)", id)
	conn.Exec("DELETE FROM collections WHERE id = ?", id)
	slog.Info("deleted collection", "name", name)
}

// ---------------------------------------------------------------------------
// Shared widget helpers
// ---------------------------------------------------------------------------

// newStringListWidget creates a list widget for editing a string slice.
func newStringListWidget(items *[]string, addLabel string, w fyne.Window, folderPicker bool) fyne.CanvasObject {
	list := widget.NewList(
		func() int { return len(*items) },
		func() fyne.CanvasObject { return widget.NewLabel("template") },
		func(id widget.ListItemID, o fyne.CanvasObject) {
			o.(*widget.Label).SetText((*items)[id])
		},
	)
	list.OnSelected = func(id widget.ListItemID) {
		list.UnselectAll()
	}

	var addBtn *widget.Button
	if folderPicker {
		addBtn = widget.NewButton(addLabel, func() {
			dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
				if uri != nil {
					*items = append(*items, uri.Path())
					list.Refresh()
				}
			}, w)
		})
	} else {
		addBtn = widget.NewButton(addLabel, func() {
			entry := widget.NewEntry()
			dialog.ShowForm(addLabel, "Add", "Cancel",
				[]*widget.FormItem{widget.NewFormItem("Value", entry)},
				func(ok bool) {
					if ok && entry.Text != "" {
						*items = append(*items, entry.Text)
						list.Refresh()
					}
				}, w)
		})
	}

	removeBtn := widget.NewButton("Remove Last", func() {
		if len(*items) > 0 {
			*items = (*items)[:len(*items)-1]
			list.Refresh()
		}
	})

	buttons := container.NewHBox(addBtn, removeBtn)
	return container.NewBorder(nil, buttons, nil, nil, withMinHeight(list, 120))
}

// intValidator returns a fyne validator for integer values in [min, max].
func intValidator(min, max int) fyne.StringValidator {
	return func(s string) error {
		if s == "" {
			return nil
		}
		v, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("must be a number")
		}
		if v < min || v > max {
			return fmt.Errorf("must be between %d and %d", min, max)
		}
		return nil
	}
}
