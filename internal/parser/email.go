package parser

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// EmailMessage is a parsed email from eM Client.
type EmailMessage struct {
	Subject    string
	BodyText   string
	Sender     string
	Recipients []string
	Date       string // ISO datetime
	Folder     string
	MessageID  string
}

var (
	uuidRE      = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	quotedRE    = regexp.MustCompile(`(?m)^>.*$`)
	onWroteRE   = regexp.MustCompile(`(?im)^On\s+.{10,80}\s+wrote:\s*$`)
	sigDelimRE  = regexp.MustCompile(`(?m)^-- $`)
	sentFromRE  = regexp.MustCompile(`(?im)^Sent from my (?:iPhone|iPad|Android|Galaxy).*$`)
	getOutlkRE  = regexp.MustCompile(`(?im)^Get Outlook for .*$`)
	excessNLRE  = regexp.MustCompile(`\n{3,}`)
)

const (
	addrTypeFrom = 1
	addrTypeTo   = 4
	addrTypeCC   = 5
)

// parseDateTicks converts a raw date column value to .NET ticks (int64).
// CAST(date AS INTEGER) in the query ensures go-sqlite3 returns int64,
// but we handle other types defensively.
func parseDateTicks(raw any) int64 {
	if raw == nil {
		return 0
	}
	switch v := raw.(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	default:
		slog.Warn("unexpected date column type", "type", fmt.Sprintf("%T", raw), "value", raw)
		return 0
	}
}

// .NET epoch: 0001-01-01
var dotnetEpoch = time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)

// FindEmailAccountDirs finds eM Client account directories containing mail databases.
func FindEmailAccountDirs(basePath string) []string {
	var dirs []string

	entries, err := os.ReadDir(basePath)
	if err != nil {
		return dirs
	}

	for _, entry := range entries {
		if !entry.IsDir() || !uuidRE.MatchString(entry.Name()) {
			continue
		}
		subPath := filepath.Join(basePath, entry.Name())
		subEntries, err := os.ReadDir(subPath)
		if err != nil {
			continue
		}
		for _, sub := range subEntries {
			if !sub.IsDir() || !uuidRE.MatchString(sub.Name()) {
				continue
			}
			accountDir := filepath.Join(subPath, sub.Name())
			if fileExists(filepath.Join(accountDir, "mail_index.dat")) {
				dirs = append(dirs, accountDir)
			}
		}
	}

	return dirs
}

// ParseEmails parses emails from an eM Client account directory.
func ParseEmails(accountDir string, sinceDate string) ([]*EmailMessage, error) {
	mailIndexPath := filepath.Join(accountDir, "mail_index.dat")
	if !fileExists(mailIndexPath) {
		return nil, fmt.Errorf("mail_index.dat not found in %s", accountDir)
	}

	conn, err := openReadOnly(mailIndexPath)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	folders := loadFolders(accountDir)
	ftiContent := loadFTIContent(accountDir)
	addresses := loadAddresses(conn)

	slog.Info("loaded email data",
		"folders", len(folders),
		"bodies", len(ftiContent),
		"addresses", len(addresses),
	)

	query := "SELECT id, folder, CAST(date AS INTEGER), subject, messageId, preview FROM MailItems"
	var args []any

	if sinceDate != "" {
		ticks := isoToTicks(sinceDate)
		if ticks > 0 {
			query += " WHERE CAST(date AS INTEGER) > ?"
			args = append(args, ticks)
		}
	}
	query += " ORDER BY date ASC"

	rows, err := conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query MailItems: %w", err)
	}
	defer rows.Close()

	messages := make([]*EmailMessage, 0)
	var errorCount int

	for rows.Next() {
		var id int64
		var folderID sql.NullInt64
		var dateRaw any
		var subject, messageID sql.NullString
		var preview sql.NullString

		if err := rows.Scan(&id, &folderID, &dateRaw, &subject, &messageID, &preview); err != nil {
			errorCount++
			if errorCount <= 10 {
				slog.Warn("error scanning email row", "err", err)
			}
			continue
		}

		dateTicks := parseDateTicks(dateRaw)

		msg := rowToEmail(id, folderID, dateTicks, subject, messageID, preview,
			folders, ftiContent, addresses)
		if msg != nil {
			messages = append(messages, msg)
		}
	}

	slog.Info("parsed emails", "count", len(messages), "errors", errorCount)
	return messages, rows.Err()
}

// ticksPerSecond is 10,000,000 (.NET ticks are 100-nanosecond intervals).
const ticksPerSecond = 10_000_000

// unixEpochTicks is the .NET ticks value at Unix epoch (1970-01-01).
// Calculated as: (1970-1) * 365.2425 * 86400 * 10_000_000, but the exact
// value from .NET is 621355968000000000.
const unixEpochTicks int64 = 621_355_968_000_000_000

func ticksToISO(ticks int64) string {
	if ticks == 0 {
		return ""
	}
	// Convert .NET ticks to Unix seconds to avoid time.Duration overflow.
	unixSeconds := (ticks - unixEpochTicks) / ticksPerSecond
	dt := time.Unix(unixSeconds, 0).UTC()
	return dt.Format(time.RFC3339)
}

func isoToTicks(isoDate string) int64 {
	var dt time.Time
	var err error
	if strings.Contains(isoDate, "T") {
		dt, err = time.Parse(time.RFC3339, isoDate)
	} else {
		dt, err = time.Parse("2006-01-02", isoDate)
	}
	if err != nil {
		return 0
	}
	return dt.Unix()*ticksPerSecond + unixEpochTicks
}

func stripQuotedReplies(text string) string {
	if loc := onWroteRE.FindStringIndex(text); loc != nil {
		text = strings.TrimSpace(text[:loc[0]])
	}
	text = quotedRE.ReplaceAllString(text, "")
	text = excessNLRE.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}

func stripSignature(text string) string {
	if loc := sigDelimRE.FindStringIndex(text); loc != nil {
		text = strings.TrimSpace(text[:loc[0]])
	}
	for _, re := range []*regexp.Regexp{sentFromRE, getOutlkRE} {
		if loc := re.FindStringIndex(text); loc != nil {
			text = strings.TrimSpace(text[:loc[0]])
		}
	}
	return text
}

func openReadOnly(dbPath string) (*sql.DB, error) {
	uri := fmt.Sprintf("file:%s?mode=ro", dbPath)
	db, err := sql.Open("sqlite3", uri)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dbPath, err)
	}
	return db, nil
}

func loadFolders(accountDir string) map[int64]string {
	foldersPath := filepath.Join(accountDir, "folders.dat")
	conn, err := openReadOnly(foldersPath)
	if err != nil {
		return map[int64]string{}
	}
	defer conn.Close()

	rows, err := conn.Query("SELECT id, path, name FROM Folders")
	if err != nil {
		slog.Warn("cannot read folders.dat", "err", err)
		return map[int64]string{}
	}
	defer rows.Close()

	folders := make(map[int64]string)
	for rows.Next() {
		var id int64
		var path, name sql.NullString
		if err := rows.Scan(&id, &path, &name); err != nil {
			continue
		}
		if path.Valid && path.String != "" {
			folders[id] = path.String
		} else if name.Valid {
			folders[id] = name.String
		}
	}
	return folders
}

func loadFTIContent(accountDir string) map[int64]string {
	ftiPath := filepath.Join(accountDir, "mail_fti.dat")
	conn, err := openReadOnly(ftiPath)
	if err != nil {
		return map[int64]string{}
	}
	defer conn.Close()

	rows, err := conn.Query(
		"SELECT id, partName, content FROM LocalMailsIndex3 ORDER BY id, partName",
	)
	if err != nil {
		slog.Warn("cannot read mail_fti.dat", "err", err)
		return map[int64]string{}
	}
	defer rows.Close()

	content := make(map[int64]string)
	for rows.Next() {
		var id int64
		var partName sql.NullInt64
		var text sql.NullString
		if err := rows.Scan(&id, &partName, &text); err != nil {
			continue
		}
		if !text.Valid || strings.TrimSpace(text.String) == "" {
			continue
		}
		// Keep first non-empty content per ID (partName=1 preferred).
		if _, exists := content[id]; !exists {
			content[id] = text.String
		}
	}
	return content
}

func loadAddresses(conn *sql.DB) map[int64]map[string][]string {
	rows, err := conn.Query(
		"SELECT parentId, type, displayName, address FROM MailAddresses ORDER BY parentId, type, position",
	)
	if err != nil {
		slog.Warn("cannot read MailAddresses", "err", err)
		return map[int64]map[string][]string{}
	}
	defer rows.Close()

	addresses := make(map[int64]map[string][]string)
	for rows.Next() {
		var mailID int64
		var addrType int
		var displayName, address sql.NullString
		if err := rows.Scan(&mailID, &addrType, &displayName, &address); err != nil {
			continue
		}
		if !address.Valid || address.String == "" {
			continue
		}

		if _, ok := addresses[mailID]; !ok {
			addresses[mailID] = map[string][]string{"from": {}, "to": {}, "cc": {}}
		}

		formatted := address.String
		if displayName.Valid && displayName.String != "" {
			formatted = displayName.String + " <" + address.String + ">"
		}

		switch addrType {
		case addrTypeFrom:
			addresses[mailID]["from"] = append(addresses[mailID]["from"], formatted)
		case addrTypeTo:
			addresses[mailID]["to"] = append(addresses[mailID]["to"], formatted)
		case addrTypeCC:
			addresses[mailID]["cc"] = append(addresses[mailID]["cc"], formatted)
		}
	}
	return addresses
}

func rowToEmail(
	id int64,
	folderID sql.NullInt64,
	dateTicks int64,
	subject, messageID sql.NullString,
	preview sql.NullString,
	folders map[int64]string,
	ftiContent map[int64]string,
	addresses map[int64]map[string][]string,
) *EmailMessage {
	subjectStr := ""
	if subject.Valid {
		subjectStr = subject.String
	}
	msgID := ""
	if messageID.Valid {
		msgID = messageID.String
	}
	dateStr := ticksToISO(dateTicks)

	folder := ""
	if folderID.Valid {
		folder = folders[folderID.Int64]
	}

	addrData, _ := addresses[id]
	if addrData == nil {
		addrData = map[string][]string{"from": {}, "to": {}, "cc": {}}
	}
	sender := ""
	if len(addrData["from"]) > 0 {
		sender = addrData["from"][0]
	}
	recipients := append(addrData["to"], addrData["cc"]...)

	body := ftiContent[id]
	if body == "" && preview.Valid {
		body = preview.String
	}
	if body == "" && subjectStr == "" {
		return nil
	}

	body = stripQuotedReplies(body)
	body = stripSignature(body)

	if strings.TrimSpace(body) == "" && subjectStr == "" {
		return nil
	}

	if msgID == "" {
		h := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s", sender, subjectStr, dateStr)))
		msgID = fmt.Sprintf("%x", h[:16])
	}

	return &EmailMessage{
		Subject:    subjectStr,
		BodyText:   body,
		Sender:     sender,
		Recipients: recipients,
		Date:       dateStr,
		Folder:     folder,
		MessageID:  msgID,
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
