package services

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"encoding/xml"

	"github.com/nickng/bibtex"
)

type PaperLoader struct{}

func NewPaperLoader() *PaperLoader {
	return &PaperLoader{}
}

func (pl *PaperLoader) ProcessPaper(arxivID string) (map[string]interface{}, error) {
	sourceContent, err := pl.downloadPaper(arxivID)
	if err != nil {
		return nil, fmt.Errorf("failed to download source for paper with ID: %s: %v", arxivID, err)
	}

	bibFiles, err := pl.extractBibFiles(sourceContent)
	if err != nil {
		return nil, fmt.Errorf("failed to extract bib files for paper with ID: %s: %v", arxivID, err)
	}

	if len(bibFiles) == 0 {
		return nil, fmt.Errorf("no .bib files found for paper with ID: %s", arxivID)
	}

	references, err := pl.parseBibFiles(bibFiles)
	if err != nil {
		return nil, fmt.Errorf("failed to parse bib files for paper with ID: %s: %v", arxivID, err)
	}

	formattedReferences := pl.formatReferences(references)

	metadata, err := pl.getPaperMetadata(arxivID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch metadata for paper with ID: %s: %v", arxivID, err)
	}

	result := map[string]interface{}{
		"title":      metadata["title"],
		"authors":    metadata["authors"],
		"abstract":   metadata["abstract"],
		"pdf_url":    metadata["pdf_url"],
		"references": formattedReferences,
		"arxiv_id":   arxivID,
	}

	return result, nil
}

func (pl *PaperLoader) downloadPaper(arxivID string) (io.Reader, error) {
	resp, err := http.Get(fmt.Sprintf("https://arxiv.org/e-print/%s", arxivID))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return resp.Body, nil
}

func (pl *PaperLoader) extractBibFiles(reader io.Reader) ([]string, error) {
	gzr, err := gzip.NewReader(reader)
	if err != nil {
		return nil, err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	var bibFiles []string
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		if strings.HasSuffix(header.Name, ".bib") {
			content, err := io.ReadAll(tr)
			if err != nil {
				return nil, err
			}
			bibFiles = append(bibFiles, string(content))
		}
	}

	return bibFiles, nil
}

func (pl *PaperLoader) parseBibFiles(bibFiles []string) ([]bibtex.BibEntry, error) {
	var allReferences []bibtex.BibEntry
	for _, content := range bibFiles {
		bib, err := bibtex.Parse(strings.NewReader(content))
		if err != nil {
			return nil, err
		}
		for _, entry := range bib.Entries {
			allReferences = append(allReferences, *entry)
		}
	}
	return allReferences, nil
}

func (pl *PaperLoader) formatReferences(references []bibtex.BibEntry) []map[string]interface{} {
	var formattedReferences []map[string]interface{}
	for _, ref := range references {
		formattedRef := pl.formatReference(ref)
		detectedArxivID := pl.detectArxivID(formattedRef)
		formattedReferences = append(formattedReferences, map[string]interface{}{
			"text":                  formattedRef,
			"arxiv_id":              detectedArxivID,
			"is_available_on_arxiv": detectedArxivID != "",
		})
	}
	return formattedReferences
}

func (pl *PaperLoader) formatReference(entry bibtex.BibEntry) string {
	authors := entry.Fields["author"]
	title := entry.Fields["title"]
	year := entry.Fields["year"]
	journalField := entry.Fields["journal"]
	if journalField == nil {
		journalField = entry.Fields["booktitle"]
	}
	journal := journalField.String()

	return fmt.Sprintf("%s. (%s). %s. %s", authors, year, title, journal)
}

func (pl *PaperLoader) detectArxivID(reference string) string {
	arxivPattern := `(?i)(?:arxiv:|https?://arxiv.org/abs/)(\d{4}\.\d{4,5})`
	re := regexp.MustCompile(arxivPattern)
	match := re.FindStringSubmatch(reference)
	if len(match) > 1 {
		return match[1]
	}
	return ""
}

// ArxivEntry represents the structure of an entry in the arXiv API response
type ArxivEntry struct {
	Title     string `xml:"title"`
	Summary   string `xml:"summary"`
	Published string `xml:"published"`
	Updated   string `xml:"updated"`
	Authors   []struct {
		Name string `xml:"name"`
	} `xml:"author"`
	Links []struct {
		Href string `xml:"href,attr"`
		Rel  string `xml:"rel,attr"`
		Type string `xml:"type,attr"`
	} `xml:"link"`
}

// ArxivFeed represents the structure of the arXiv API response
type ArxivFeed struct {
	Entry ArxivEntry `xml:"entry"`
}

func (pl *PaperLoader) getPaperMetadata(arxivID string) (map[string]string, error) {
	url := fmt.Sprintf("http://export.arxiv.org/api/query?id_list=%s", arxivID)

	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch arXiv metadata: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	var feed ArxivFeed
	err = xml.Unmarshal(body, &feed)
	if err != nil {
		return nil, fmt.Errorf("failed to parse XML response: %v", err)
	}

	entry := feed.Entry

	// Extract authors
	var authors []string
	for _, author := range entry.Authors {
		authors = append(authors, author.Name)
	}

	// Find PDF URL
	pdfURL := ""
	for _, link := range entry.Links {
		if link.Type == "application/pdf" {
			pdfURL = link.Href
			break
		}
	}

	metadata := map[string]string{
		"title":          entry.Title,
		"authors":        strings.Join(authors, ", "),
		"abstract":       entry.Summary,
		"pdf_url":        pdfURL,
		"published_date": entry.Published,
		"last_updated":   entry.Updated,
	}

	return metadata, nil
}
