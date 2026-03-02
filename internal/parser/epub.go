package parser

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"log/slog"
	"path"
	"strings"
)

// ChapterText represents a single chapter of extracted EPUB text.
type ChapterText struct {
	ChapterNumber int // 1-based
	Text          string
}

// ParseEPUB extracts text from an EPUB file, chapter by chapter.
func ParseEPUB(filePath string) []ChapterText {
	reader, err := zip.OpenReader(filePath)
	if err != nil {
		slog.Error("failed to open EPUB", "path", filePath, "err", err)
		return nil
	}
	defer reader.Close()

	opfPath := findOPFPath(reader)
	if opfPath == "" {
		slog.Warn("could not find OPF file in EPUB", "path", filePath)
		return nil
	}

	opfDir := path.Dir(opfPath)
	spineHrefs := parseOPFSpine(reader, opfPath)
	if len(spineHrefs) == 0 {
		slog.Warn("no spine items found in EPUB", "path", filePath)
		return nil
	}

	var chapters []ChapterText
	for chapterNum, href := range spineHrefs {
		fullPath := href
		if opfDir != "." && opfDir != "" {
			fullPath = opfDir + "/" + href
		}

		content, err := readZipFile(reader, fullPath)
		if err != nil {
			slog.Debug("spine item not found in archive", "item", fullPath)
			continue
		}

		text := HTMLToText(content)
		if strings.TrimSpace(text) != "" {
			chapters = append(chapters, ChapterText{
				ChapterNumber: chapterNum + 1,
				Text:          strings.TrimSpace(text),
			})
		}
	}

	if len(chapters) == 0 {
		slog.Warn("no extractable text found in EPUB", "path", filePath)
	}

	return chapters
}

// container.xml structures
type containerXML struct {
	Rootfiles struct {
		Rootfile []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfile"`
	} `xml:"rootfiles"`
}

func findOPFPath(reader *zip.ReadCloser) string {
	content, err := readZipFile(reader, "META-INF/container.xml")
	if err != nil {
		return ""
	}

	var container containerXML
	if err := xml.Unmarshal([]byte(content), &container); err != nil {
		slog.Warn("failed to parse container.xml", "err", err)
		return ""
	}

	if len(container.Rootfiles.Rootfile) > 0 {
		return container.Rootfiles.Rootfile[0].FullPath
	}
	return ""
}

// OPF structures
type opfPackage struct {
	Manifest struct {
		Items []struct {
			ID        string `xml:"id,attr"`
			Href      string `xml:"href,attr"`
			MediaType string `xml:"media-type,attr"`
		} `xml:"item"`
	} `xml:"manifest"`
	Spine struct {
		Itemrefs []struct {
			IDRef string `xml:"idref,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
}

func parseOPFSpine(reader *zip.ReadCloser, opfPath string) []string {
	content, err := readZipFile(reader, opfPath)
	if err != nil {
		return nil
	}

	var pkg opfPackage
	if err := xml.Unmarshal([]byte(content), &pkg); err != nil {
		slog.Warn("failed to parse OPF", "err", err)
		return nil
	}

	// Build manifest: id -> (href, media-type)
	type manifestItem struct {
		href      string
		mediaType string
	}
	manifest := make(map[string]manifestItem)
	for _, item := range pkg.Manifest.Items {
		if item.ID != "" && item.Href != "" {
			manifest[item.ID] = manifestItem{href: item.Href, mediaType: item.MediaType}
		}
	}

	var spineHrefs []string
	for _, itemref := range pkg.Spine.Itemrefs {
		item, ok := manifest[itemref.IDRef]
		if !ok {
			continue
		}
		if strings.Contains(item.mediaType, "html") || strings.Contains(item.mediaType, "xml") {
			spineHrefs = append(spineHrefs, item.href)
		}
	}

	return spineHrefs
}

func readZipFile(reader *zip.ReadCloser, name string) (string, error) {
	for _, f := range reader.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			defer rc.Close()
			var sb strings.Builder
			buf := make([]byte, 4096)
			for {
				n, err := rc.Read(buf)
				if n > 0 {
					sb.Write(buf[:n])
				}
				if err != nil {
					break
				}
			}
			return sb.String(), nil
		}
	}
	return "", fmt.Errorf("file not found in archive: %s", name)
}
