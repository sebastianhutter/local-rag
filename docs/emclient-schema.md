# eM Client Database Schema

Findings from exploring the eM Client SQLite databases on macOS. This documents the data structures local-rag uses to index emails.

## Directory Structure

eM Client stores data under `~/Library/Application Support/eM Client/`. Each email account gets its own nested UUID directory pair:

```
~/Library/Application Support/eM Client/
    accounts.dat                    # Account configuration (key-value, not mail data)
    categories.dat
    settings.dat
    ...
    <account-uuid>/
        <sub-uuid>/
            mail_index.dat          # Email metadata + addresses (primary source)
            mail_fti.dat            # Pre-extracted body text (full-text index)
            folders.dat             # Folder tree
            mail_data.dat           # Raw MIME parts (fallback)
            mail_fti.dat            # eM Client's own full-text index
            attachments.dat         # Attachment data
            contact_data.dat        # Contacts
            event_data.dat          # Calendar events
            ...
        <account-name>.acn          # Account name file
    <account-uuid>/
        <sub-uuid>/
            ...                     # Same structure per account
```

Both the account UUID and sub-UUID directories follow the UUID4 format (`xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx`). An account directory is identified by the presence of `mail_index.dat`.

## Database Files Used by local-rag

local-rag reads three files per account (all opened in read-only mode):

### mail_index.dat

The primary metadata source. Contains structured email data across several tables.

#### MailItems

One row per email. This is the main table.

| Column | Type | Description |
| --- | --- | --- |
| id | INTEGER | Primary key, used to join with other tables |
| uniqueId | TEXT | eM Client internal unique ID |
| folder | INTEGER | Foreign key to Folders table in folders.dat |
| date | INTEGER | Send date as .NET ticks (see Date Format below) |
| subject | TEXT | Email subject line |
| messageId | TEXT | RFC 2822 Message-ID header value |
| importance | INTEGER | Email priority |
| flags | INTEGER | Read/unread, flagged, etc. |
| size | INTEGER | Email size in bytes |
| receivedDate | INTEGER | Received date as .NET ticks |
| preview | TEXT | Short plain-text preview of the body (~150 chars) |
| inReplyTo | TEXT | Message-ID of the email this replies to |
| references | TEXT | Thread reference chain |
| conversationId | TEXT | Conversation/thread grouping ID |
| notes | TEXT | User-added notes |
| classification | TEXT | Email classification |

Other columns exist (`versionId`, `syncFlags`, `syncFolder`, `account`, `editTime`, `operationsPerformed`, `replyDate`, `forwardDate`, `downloadState`, `lastMarkAsRead`, `sendAfter`, `showAfterDate`, `imipDate`, `imipEndDate`) but are not used by local-rag.

#### MailAddresses

One row per address per email. Links to MailItems via `parentId`.

| Column | Type | Description |
| --- | --- | --- |
| id | INTEGER | Primary key |
| parentId | INTEGER | Foreign key to MailItems.id |
| type | INTEGER | Address type (see below) |
| position | INTEGER | Order within the type (0-indexed) |
| displayName | TEXT | Human-readable name |
| address | TEXT | Email address |

**Address type codes:**

| Type | Meaning | Notes |
| --- | --- | --- |
| 1 | From | Display name variant of the sender |
| 2 | Sender | Technical sender (usually same as From) |
| 3 | Reply-To | Reply-to address |
| 4 | To | Primary recipient(s) |
| 5 | CC | Carbon copy recipient(s) |
| 6 | BCC | Blind carbon copy (rare, only 4 found in test data) |

local-rag uses type 1 (From) for the sender and types 4+5 (To + CC) for recipients.

#### Other Tables

| Table | Description |
| --- | --- |
| MailCategoryNames | Category/label assignments per email |
| FlaggedMailItems | IDs of flagged emails |
| MailCounts | Unread/recent counts per folder |
| MailCategoryUnreadCounts | Unread counts per category |
| MailNotification | Reply notification settings |
| SnoozedMailItems | Snoozed email IDs |

### mail_fti.dat

eM Client's own full-text index. Contains pre-extracted plain text from email bodies — much easier to use than parsing raw MIME from `mail_data.dat`.

#### LocalMailsIndex3

| Column | Type | Description |
| --- | --- | --- |
| id | INTEGER | Matches MailItems.id |
| partName | TEXT | MIME part identifier ("1" = plain text, "2" = HTML-extracted text) |
| content | TEXT | Extracted text content |

Each email may have multiple rows (one per MIME part). local-rag prefers `partName='1'` (plain text extraction) and falls back to `partName='2'` (text extracted from HTML).

Not every email has an entry here. Of 7,752 emails in the test account, 6,341 had FTI content. For the remaining ~18%, local-rag falls back to the `preview` field from MailItems.

The table also has FTS support tables (`_content`, `_segments`, `_segdir`, `_docsize`, `_stat`) which are internal to SQLite FTS and not read directly.

Other FTS indexes in this file (`MailSubjectIndex`, `MailAddressIndex`, `MailNotesIndex`) are not used by local-rag.

### folders.dat

Folder hierarchy for the account.

#### Folders

| Column | Type | Description |
| --- | --- | --- |
| id | INTEGER | Primary key, referenced by MailItems.folder |
| uniqueId | TEXT | eM Client internal ID |
| name | TEXT | Display name (e.g., "INBOX", "Sent Mail") |
| path | TEXT | Full path (e.g., "/[Gmail]/All Mail", "/#_Archive/Projects") |
| parentFolderId | INTEGER | Parent folder ID for tree structure |
| delimiter | TEXT | Path separator character |

Example folder tree (Gmail account):

```
id=1   /              (Root)
id=2   /INBOX         (INBOX)
id=3   /[Gmail]       ([Gmail])
id=4   /[Gmail]/All Mail
id=5   /[Gmail]/Drafts
id=6   /[Gmail]/Important
id=7   /[Gmail]/Sent Mail
id=8   /[Gmail]/Spam
id=9   /[Gmail]/Trash
id=10  /#_Archive
id=11  /#_Archive/@ PENDING
```

#### FolderAttributes

Key-value metadata per folder (not used by local-rag).

### mail_data.dat (not used directly)

Contains raw MIME parts. local-rag does not read this file because `mail_index.dat` + `mail_fti.dat` provide all needed data in a cleaner format.

#### LocalMailContents

| Column | Type | Description |
| --- | --- | --- |
| id | INTEGER | Matches MailItems.id |
| partName | TEXT | MIME part path ("TEXT" = top-level headers, "1" = first part, "2" = second part, "1.1" = nested) |
| contentType | TEXT | MIME content type (e.g., "text/plain; charset=UTF-8", "text/html; charset=UTF-8") |
| partHeader | BLOB | RFC 2822 headers for this part (full email headers when partName="TEXT") |
| partBody | BLOB | Part body content (often empty for text/plain; HTML parts usually have data) |
| contentId | TEXT | MIME Content-ID |
| contentDisposition | TEXT | Attachment disposition |
| contentLength | INTEGER | Declared content length |
| contentTransferEncoding | INTEGER | Encoding type |

The `partName='TEXT'` row holds the complete RFC 2822 email headers in `partHeader` (From, To, Subject, Date, all transport headers). The body data is unreliable — most `text/plain` parts have empty `partBody` (only 2 out of 6,185 had data in the test account). HTML parts (`partName='2'`) more reliably contain body data.

## Date Format

Dates are stored as .NET ticks — 64-bit integers representing 100-nanosecond intervals since `0001-01-01 00:00:00 UTC`.

Conversion to Python datetime:

```python
from datetime import datetime, timedelta

DOTNET_EPOCH = datetime(1, 1, 1)

def ticks_to_datetime(ticks: int) -> datetime:
    return DOTNET_EPOCH + timedelta(microseconds=ticks / 10)

# Example: 639052111470000000 → 2026-01-28 15:32:27
```

A date value of `0` means no date was recorded for that email.

## Observed Data Volumes

From a test installation with 6 accounts:

| Account | Emails | FTI Bodies | Folders |
| --- | --- | --- | --- |
| Account 1 (Gmail) | 7,752 | 6,341 | 67 |
| Account 2 | 3 | 3 | 14 |
| Account 3 | 1,102 | 1,038 | 9 |
| Account 4 (primary) | 26,417 | 25,666 | 18 |
| Account 5 | 10,917 | 10,453 | 25 |
| Account 6 | 6,678 | 6,403 | 9 |
| **Total** | **52,869** | **49,904** | **142** |

~94% of emails have pre-extracted body text in `mail_fti.dat`. The remaining ~6% use the `preview` field from MailItems as a fallback.
