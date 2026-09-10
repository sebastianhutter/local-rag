package indexer

import (
	"fmt"
	"testing"
	"time"

	"github.com/sebastianhutter/local-rag-go/internal/parser"
)

func TestEmailToItem(t *testing.T) {
	email := &parser.EmailMessage{
		MessageID:  "msg-42",
		Subject:    "Invoice attached",
		BodyText:   "Please find the invoice attached.",
		Sender:     "supplier@example.com",
		Recipients: []string{"me@example.com"},
		Date:       "2026-08-16T09:11:56Z",
		Folder:     "Inbox",
	}

	item := emailToItem(email, testBatchConfig(32))

	if item.SourcePath != "msg-42" {
		t.Errorf("SourcePath = %q, want the message ID", item.SourcePath)
	}
	if item.Title != "Invoice attached" {
		t.Errorf("Title = %q", item.Title)
	}
	if len(item.Chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}

	for key, want := range map[string]any{
		"sender": "supplier@example.com",
		"date":   "2026-08-16T09:11:56Z",
		"folder": "Inbox",
	} {
		if got := item.Metadata[key]; got != want {
			t.Errorf("metadata[%q] = %v, want %v", key, got, want)
		}
	}

	rcpts, ok := item.Metadata["recipients"].([]string)
	if !ok || len(rcpts) != 1 || rcpts[0] != "me@example.com" {
		t.Errorf("metadata[recipients] = %v, want [me@example.com]", item.Metadata["recipients"])
	}
}

func TestEmailToItemNoSubject(t *testing.T) {
	item := emailToItem(&parser.EmailMessage{
		MessageID: "msg-1",
		BodyText:  "body with no subject",
	}, testBatchConfig(32))

	if item.Title != "(no subject)" {
		t.Errorf("Title = %q, want the no-subject placeholder", item.Title)
	}
}

func TestEmailToItemSetsSourceType(t *testing.T) {
	item := emailToItem(&parser.EmailMessage{
		MessageID: "m1", Subject: "S", BodyText: "body",
	}, testBatchConfig(32))

	if item.SourceType != "email" {
		t.Errorf("SourceType = %q, want email", item.SourceType)
	}
}

// A message scheduled for the future is indexed like any other, but its date
// must not become the watermark: everything arriving before its send date would
// then sit below the watermark and never be fetched again.
func TestEmailWatermarkIgnoresFutureDates(t *testing.T) {
	conn := setupTestDB(t)
	collID := mustGetOrCreate(t, conn, "email", "system")

	past := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	future := time.Now().UTC().Add(72 * time.Hour).Format(time.RFC3339)

	for i, date := range []string{past, future} {
		res, err := conn.Exec(
			"INSERT INTO sources (collection_id, source_type, source_path) VALUES (?, 'email', ?)",
			collID, fmt.Sprintf("msg-%d", i),
		)
		if err != nil {
			t.Fatalf("insert source: %v", err)
		}
		sourceID, _ := res.LastInsertId()
		if _, err := conn.Exec(
			"INSERT INTO documents (source_id, collection_id, chunk_index, title, content, metadata) VALUES (?, ?, 0, 'subject', 'body', ?)",
			sourceID, collID, fmt.Sprintf(`{"date":%q}`, date),
		); err != nil {
			t.Fatalf("insert document: %v", err)
		}
	}

	if got := getEmailWatermark(conn, collID); got != past {
		t.Errorf("watermark = %q, want %q (the newest non-future date)", got, past)
	}
}
