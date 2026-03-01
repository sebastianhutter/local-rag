package gui

import (
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

func (a *App) openLogs() {
	// Raise existing window if already open.
	if a.logsWin != nil {
		a.logsWin.Show()
		a.logsWin.RequestFocus()
		return
	}

	w := a.fyneApp.NewWindow("local-rag Logs")
	w.Resize(fyne.NewSize(800, 500))
	a.logsWin = w

	// Multiline entry for log output. Not disabled (disabled text is dimmed
	// and unreadable on dark themes). Content is overwritten every 100ms so
	// any accidental keystrokes are immediately replaced.
	logEntry := widget.NewMultiLineEntry()
	logEntry.TextStyle.Monospace = true
	logEntry.Wrapping = fyne.TextWrapWord

	// Load existing history.
	history := a.logHandler.GetHistory()
	if len(history) > 0 {
		logEntry.SetText(strings.Join(history, "\n") + "\n")
	}

	// Auto-scroll state.
	autoScroll := true
	autoScrollBtn := widget.NewButton("Auto-scroll: ON", nil)
	autoScrollBtn.OnTapped = func() {
		autoScroll = !autoScroll
		if autoScroll {
			autoScrollBtn.SetText("Auto-scroll: ON")
		} else {
			autoScrollBtn.SetText("Auto-scroll: OFF")
		}
	}

	clearBtn := widget.NewButton("Clear", func() {
		logEntry.SetText("")
		a.logHandler.Clear()
	})

	toolbar := container.NewHBox(autoScrollBtn, clearBtn)

	// Subscribe to new log lines.
	subID, ch := a.logHandler.Subscribe()

	// Batch updates goroutine — UI mutations go through fyne.Do.
	stopCh := make(chan struct{})
	go func() {
		var batch []string
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case line, ok := <-ch:
				if !ok {
					return
				}
				batch = append(batch, line)
			case <-ticker.C:
				if len(batch) == 0 {
					continue
				}
				lines := strings.Join(batch, "\n") + "\n"
				batch = batch[:0]
				scroll := autoScroll
				fyne.Do(func() {
					logEntry.SetText(logEntry.Text + lines)
					if scroll {
						logEntry.CursorRow = len(strings.Split(logEntry.Text, "\n")) - 1
					}
				})
			case <-stopCh:
				return
			}
		}
	}()

	w.SetContent(container.NewBorder(nil, toolbar, nil, nil,
		container.NewScroll(logEntry),
	))

	w.SetOnClosed(func() {
		close(stopCh)
		a.logHandler.Unsubscribe(subID)
		a.logsWin = nil
	})

	w.Show()
}
