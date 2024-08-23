package services

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"nexus_scholar_go_backend/internal/database"
	"nexus_scholar_go_backend/internal/errors"
	"nexus_scholar_go_backend/internal/models"
	"regexp"
	"strings"

	"encoding/xml"

	"github.com/nickng/bibtex"
	"github.com/rs/zerolog"
)

type PaperLoader struct {
	logger zerolog.Logger
}

type simpleBibString string

func (s simpleBibString) String() string    { return string(s) }
func (s simpleBibString) RawString() string { return string(s) }

func NewPaperLoader(logger zerolog.Logger) *PaperLoader {
	return &PaperLoader{logger: logger}
}

func (pl *PaperLoader) ProcessPaper(arxivID string) (map[string]interface{}, error) {
	// Wrap the entire function in a panic recovery
	defer func() {
		if r := recover(); r != nil {
			pl.logger.Error().Msgf("Panic occurred during paper processing: %v", r)
			// You might want to return an error here or handle it in some way
		}
	}()

	pl.logger.Info().Msgf("Processing paper with ArxivID: %s", arxivID)

	// Check if the paper is already in the database
	existingPaper, err := pl.GetPaperByArxivID(arxivID)
	if err == nil && existingPaper != nil {
		// Paper exists, retrieve its references
		references, err := GetReferencesByArxivID(arxivID)
		if err != nil {
			pl.logger.Error().Err(err).Msgf("Failed to retrieve references for existing paper with ArxivID: %s", arxivID)
			return nil, fmt.Errorf("failed to retrieve references for existing paper with ArxivID: %s: %v", arxivID, err)
		}

		// Format the existing references
		formattedReferences := pl.formatExistingReferences(references)

		pl.logger.Info().Msgf("Formatted %d references for paper %s", len(formattedReferences), arxivID)

		// Return the existing paper data
		return map[string]interface{}{
			"title":      existingPaper.Title,
			"authors":    strings.Split(existingPaper.Authors, ", "),
			"abstract":   existingPaper.Abstract,
			"pdf_url":    existingPaper.URL,
			"references": formattedReferences,
			"arxiv_id":   arxivID,
		}, nil
	}

	sourceContent, err := pl.downloadPaper(arxivID)
	if err != nil {
		pl.logger.Error().Err(err).Msgf("Failed to download source for paper with ID: %s", arxivID)
		return nil, fmt.Errorf("failed to download source for paper with ID: %s: %v", arxivID, err)
	}

	bibFiles, err := pl.extractBibFiles(sourceContent)
	if err != nil {
		pl.logger.Warn().Err(err).Msgf("Failed to extract bib files for paper with ID: %s, will attempt to use .bbl file", arxivID)
	}

	var references []bibtex.BibEntry
	if len(bibFiles) > 0 {
		references, err = pl.parseBibFiles(bibFiles)
		if err != nil {
			pl.logger.Error().Err(err).Msgf("Failed to parse bib files for paper with ID: %s", arxivID)
			return nil, fmt.Errorf("failed to parse bib files for paper with ID: %s: %v", arxivID, err)
		}
	} else {
		pl.logger.Info().Msgf("No .bib files found for paper with ID: %s, falling back to .bbl file", arxivID)
		bblContent, err := pl.extractBBLFile(sourceContent)
		if err != nil {
			pl.logger.Error().Err(err).Msgf("Failed to extract .bbl file for paper with ID: %s", arxivID)
			return nil, fmt.Errorf("failed to extract .bbl file for paper with ID: %s: %v", arxivID, err)
		}
		references, err = pl.parseBBLFile(bblContent)
		if err != nil {
			pl.logger.Error().Err(err).Msgf("Failed to parse .bbl file for paper with ID: %s", arxivID)
			return nil, fmt.Errorf("failed to parse .bbl file for paper with ID: %s: %v", arxivID, err)
		}
	}

	// Convert []bibtex.BibEntry to []*bibtex.BibEntry
	var refPointers []*bibtex.BibEntry
	for i := range references {
		refPointers = append(refPointers, &references[i])
	}

	formattedReferences := pl.formatReferences(refPointers)

	// Now create and save references to the database
	for i, formattedRef := range formattedReferences {
		dbRef := models.PaperReference{
			ArxivID:            formattedRef["arxiv_id"].(string),
			ParentArxivID:      arxivID,
			Type:               references[i].Type,
			Key:                references[i].CiteName,
			Title:              pl.getField(references[i], "title"),
			Author:             pl.getField(references[i], "author"),
			Year:               pl.getField(references[i], "year"),
			Journal:            pl.getField(references[i], "journal"),
			Volume:             pl.getField(references[i], "volume"),
			Number:             pl.getField(references[i], "number"),
			Pages:              pl.getField(references[i], "pages"),
			Publisher:          pl.getField(references[i], "publisher"),
			DOI:                pl.getField(references[i], "doi"),
			URL:                pl.getField(references[i], "url"),
			RawBibEntry:        references[i].String(),
			FormattedText:      formattedRef["text"].(string),
			IsAvailableOnArxiv: formattedRef["is_available_on_arxiv"].(bool),
		}
		if err := CreateOrUpdateReference(&dbRef); err != nil {
			pl.logger.Error().Err(err).Msg("Failed to save reference to database")
			return nil, fmt.Errorf("failed to save reference to database: %v", err)
		}
	}

	metadata, err := pl.GetPaperMetadata(arxivID)
	if err != nil {
		pl.logger.Error().Err(err).Msgf("Failed to fetch metadata for paper with ID: %s", arxivID)
		return nil, fmt.Errorf("failed to fetch metadata for paper with ID: %s: %v", arxivID, err)
	}

	// Create or update the paper in the database
	paper, err := CreateOrUpdatePaper(map[string]interface{}{
		"title":    metadata["title"],
		"authors":  strings.Split(metadata["authors"], ", "),
		"abstract": metadata["abstract"],
		"pdf_url":  metadata["pdf_url"],
		"arxiv_id": arxivID,
	})
	if err != nil {
		pl.logger.Error().Err(err).Msg("Failed to create or update paper")
		return nil, fmt.Errorf("failed to create or update paper: %v", err)
	}

	// Add the main paper as a reference to itself
	mainPaperRef := models.PaperReference{
		ArxivID:            arxivID,
		ParentArxivID:      arxivID,
		Type:               "article",
		Key:                arxivID,
		Title:              paper.Title,
		Author:             paper.Authors,
		Year:               metadata["published_date"][:4], // Assuming the date is in YYYY-MM-DD format
		Journal:            metadata["journal"],            // This might be empty for preprints
		DOI:                metadata["doi"],                // This might be empty for preprints
		URL:                metadata["abstract_url"],       // Using the abstract URL as the main URL
		RawBibEntry:        "",                             // You might want to generate this if needed
		FormattedText:      fmt.Sprintf("%s. (%s). %s. %s", paper.Authors, metadata["published_date"][:4], paper.Title, metadata["journal"]),
		IsAvailableOnArxiv: true,
	}

	// If there's a DOI, add it to the formatted text
	if metadata["doi"] != "" {
		mainPaperRef.FormattedText += fmt.Sprintf(" DOI: %s", metadata["doi"])
	}

	if err := CreateOrUpdateReference(&mainPaperRef); err != nil {
		pl.logger.Error().Err(err).Msg("Failed to save main paper as reference")
		return nil, fmt.Errorf("failed to save main paper as reference: %v", err)
	}

	result := map[string]interface{}{
		"title":      paper.Title,
		"authors":    strings.Split(paper.Authors, ", "),
		"abstract":   paper.Abstract,
		"pdf_url":    paper.URL,
		"references": formattedReferences,
		"arxiv_id":   paper.ArxivID,
	}

	return result, nil
}

func (pl *PaperLoader) getField(entry bibtex.BibEntry, key string) string {
	if field, ok := entry.Fields[key]; ok && field != nil {
		return field.String()
	}
	return ""
}

func (pl *PaperLoader) downloadPaper(arxivID string) ([]byte, error) {
	pl.logger.Info().Msgf("Downloading paper with ArxivID: %s", arxivID)

	resp, err := http.Get(fmt.Sprintf("https://arxiv.org/src/%s", arxivID))
	if err != nil {
		pl.logger.Error().Err(err).Msgf("Failed to download paper with ArxivID: %s", arxivID)
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		pl.logger.Error().Msgf("Failed to download paper with ArxivID: %s, status code: %d", arxivID, resp.StatusCode)
		return nil, fmt.Errorf("failed to download paper: status code %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		pl.logger.Error().Err(err).Msgf("Failed to read response body for paper with ArxivID: %s", arxivID)
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	return body, nil
}

// Methods for parsing BiB files (first choice - more detailed)

func (pl *PaperLoader) extractBibFiles(content []byte) ([]string, error) {
	pl.logger.Info().Msg("Extracting .bib files from source content")

	gzr, err := gzip.NewReader(bytes.NewReader(content))
	if err != nil {
		pl.logger.Error().Err(err).Msg("Failed to create gzip reader")
		return nil, fmt.Errorf("failed to create gzip reader: %v", err)
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
			pl.logger.Error().Err(err).Msg("Error reading tar")
			return nil, fmt.Errorf("error reading tar: %v", err)
		}

		if strings.HasSuffix(header.Name, ".bib") {
			content, err := io.ReadAll(tr)
			if err != nil {
				pl.logger.Error().Err(err).Msg("Error reading .bib file")
				return nil, fmt.Errorf("error reading .bib file: %v", err)
			}
			bibFiles = append(bibFiles, string(content))
		}
	}
	if len(bibFiles) == 0 {
		pl.logger.Error().Msg("No .bib files found in the archive")
		return nil, fmt.Errorf("no .bib files found in the archive")
	}

	return bibFiles, nil
}

func (pl *PaperLoader) parseBibFiles(bibFiles []string) ([]bibtex.BibEntry, error) {
	pl.logger.Info().Msg("Starting to parse .bib files")

	var allReferences []bibtex.BibEntry
	for i, content := range bibFiles {
		pl.logger.Debug().Msgf("Parsing .bib file %d of %d", i+1, len(bibFiles))

		// Attempt to parse the file
		bib, err := pl.safeParse(content)
		if err != nil {
			pl.logger.Warn().Err(err).Msgf("Failed to parse .bib file %d, attempting to clean and salvage", i+1)

			// Attempt to clean and salvage entries
			cleanedContent, cleanErr := pl.cleanBibContent(content)
			if cleanErr != nil {
				pl.logger.Error().Err(cleanErr).Msgf("Failed to clean .bib file %d", i+1)
				continue
			}

			// Try parsing again with cleaned content
			bib, err = pl.safeParse(cleanedContent)
			if err != nil {
				pl.logger.Error().Err(err).Msgf("Failed to parse cleaned .bib file %d", i+1)
				// If parsing still fails, attempt to salvage entries manually
				entries, salvageErr := pl.salvageBibEntries(cleanedContent)
				if salvageErr != nil {
					pl.logger.Error().Err(salvageErr).Msgf("Failed to salvage entries from .bib file %d", i+1)
					continue
				}
				bib = &bibtex.BibTex{Entries: entries}
			}
		}

		if bib != nil {
			pl.logger.Debug().Msgf("Successfully parsed .bib file %d. Found %d entries", i+1, len(bib.Entries))

			for _, entry := range bib.Entries {
				if entry != nil {
					allReferences = append(allReferences, *entry)
				}
			}
		}
	}

	pl.logger.Info().Msgf("Finished parsing all .bib files. Total references found: %d", len(allReferences))

	if len(allReferences) == 0 {
		pl.logger.Warn().Msg("No references found in any of the .bib files")
		return nil, errors.New404Error("No references found in the provided .bib files")
	}

	return allReferences, nil
}

func (pl *PaperLoader) safeParse(content string) (*bibtex.BibTex, error) {
	var bib *bibtex.BibTex
	var err error

	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panic during parsing: %v", r)
				pl.logger.Error().Msgf("Panic occurred during parsing: %v", r)
			}
		}()

		bib, err = bibtex.Parse(strings.NewReader(content))
		if err != nil {
			pl.logger.Error().Err(err).Msg("Error occurred during BibTeX parsing")
		}
	}()

	return bib, err
}

func (pl *PaperLoader) cleanBibContent(content string) (string, error) {
	lines := strings.Split(content, "\n")
	var cleanedLines []string
	for _, line := range lines {
		// Handle problematic month entries
		if strings.Contains(line, "month") {
			monthPattern := `\bmonth\s*=\s*(\w+)`
			re := regexp.MustCompile(monthPattern)
			line = re.ReplaceAllStringFunc(line, func(match string) string {
				parts := re.FindStringSubmatch(match)
				if len(parts) > 1 {
					month := strings.ToLower(parts[1])
					// Convert month names to numbers
					monthMap := map[string]string{
						"jan": "1", "feb": "2", "mar": "3", "apr": "4", "may": "5", "jun": "6",
						"jul": "7", "aug": "8", "sep": "9", "oct": "10", "nov": "11", "dec": "12",
					}
					if num, ok := monthMap[month[:3]]; ok {
						return fmt.Sprintf("month = {%s}", num)
					}
					return fmt.Sprintf("month = {%s}", month)
				}
				return match
			})
		}

		// Handle other potential issues (e.g., unquoted strings)
		line = strings.ReplaceAll(line, "= {", "= {{")
		line = strings.ReplaceAll(line, "},", "}},")

		cleanedLines = append(cleanedLines, line)
	}
	return strings.Join(cleanedLines, "\n"), nil
}

func (pl *PaperLoader) salvageBibEntries(content string) ([]*bibtex.BibEntry, error) {
	var entries []*bibtex.BibEntry
	lines := strings.Split(content, "\n")
	var currentEntry *bibtex.BibEntry
	var currentField string
	var braceCount int

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "@") {
			// Start of a new entry
			if currentEntry != nil {
				entries = append(entries, currentEntry)
			}
			currentEntry = &bibtex.BibEntry{Fields: make(map[string]bibtex.BibString)}
			parts := strings.SplitN(line, "{", 2)
			if len(parts) > 1 {
				currentEntry.Type = strings.TrimPrefix(parts[0], "@")
				currentEntry.CiteName = strings.TrimSuffix(parts[1], ",")
			}
			braceCount = 1
		} else if currentEntry != nil {
			if strings.Contains(line, "=") && braceCount == 1 {
				// New field
				parts := strings.SplitN(line, "=", 2)
				currentField = strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])
				if strings.HasPrefix(value, "{") {
					braceCount += strings.Count(value, "{") - strings.Count(value, "}")
				}
				currentEntry.Fields[currentField] = simpleBibString(value)
			} else if line == "}" && braceCount == 1 {
				// End of entry
				entries = append(entries, currentEntry)
				currentEntry = nil
				currentField = ""
			} else if currentField != "" {
				// Continuation of previous field
				braceCount += strings.Count(line, "{") - strings.Count(line, "}")
				currentValue := currentEntry.Fields[currentField].String()
				currentEntry.Fields[currentField] = simpleBibString(currentValue + " " + line)
			}
		}
	}

	if currentEntry != nil {
		entries = append(entries, currentEntry)
	}

	return entries, nil
}

// Methods for parsing .bbl file (second choice - less detailed)
func (pl *PaperLoader) extractBBLFile(content []byte) (string, error) {
	gzr, err := gzip.NewReader(bytes.NewReader(content))
	if err != nil {
		return "", fmt.Errorf("failed to create gzip reader: %v", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("error reading tar: %v", err)
		}

		if strings.HasSuffix(header.Name, ".bbl") {
			content, err := io.ReadAll(tr)
			if err != nil {
				return "", fmt.Errorf("error reading .bbl file: %v", err)
			}
			return string(content), nil
		}
	}

	return "", fmt.Errorf("no .bbl file found in the archive")
}

func (pl *PaperLoader) parseBBLFile(content string) ([]bibtex.BibEntry, error) {
	var entries []bibtex.BibEntry
	lines := strings.Split(content, "\n")

	var currentEntry bibtex.BibEntry
	var authorLines []string
	var inEntry bool
	var inAuthor bool

	for i, line := range lines {
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "\\bibitem") {
			if inEntry {
				entries = append(entries, currentEntry)
			}
			currentEntry = bibtex.BibEntry{Fields: make(map[string]bibtex.BibString)}
			currentEntry.CiteName = strings.Trim(strings.TrimPrefix(line, "\\bibitem"), "{}")
			inEntry = true
			inAuthor = true
			authorLines = []string{}
			continue
		}

		if inEntry {
			if strings.HasPrefix(line, "\\newblock") {
				if inAuthor {
					currentEntry.Fields["author"] = simpleBibString(strings.Join(authorLines, " "))
					inAuthor = false
				}

				line = strings.TrimPrefix(line, "\\newblock")

				if match := regexp.MustCompile(`\\em\s*(.*?)\s*`).FindStringSubmatch(line); len(match) > 1 {
					emContent := strings.Trim(match[1], "{},")
					if strings.Contains(emContent, "arXiv") {
						currentEntry.Fields["journal"] = simpleBibString(emContent)
						if arxivMatch := regexp.MustCompile(`arXiv:(\d{4}\.\d{4,5})`).FindStringSubmatch(emContent); len(arxivMatch) > 1 {
							currentEntry.Fields["arxiv_id"] = simpleBibString(arxivMatch[0]) //[0] to get ful lstring arxiv:2345.2093, important for later parsing
						}
					} else {
						currentEntry.Fields["journal"] = simpleBibString(emContent)
					}
				} else if _, hasTitle := currentEntry.Fields["title"]; !hasTitle {
					currentEntry.Fields["title"] = simpleBibString(strings.Trim(line, "."))
				}
			} else if inAuthor {
				authorLines = append(authorLines, line)
			}

			// Extract other details
			if year := regexp.MustCompile(`\b(\d{4})\b`).FindString(line); year != "" {
				currentEntry.Fields["year"] = simpleBibString(year)
			}
			if pages := regexp.MustCompile(`pages?\s*(\d+--?\d*)`).FindStringSubmatch(line); len(pages) > 1 {
				currentEntry.Fields["pages"] = simpleBibString(pages[1])
			}
			if volNum := regexp.MustCompile(`(\d+)\s*\((\d+)\)`).FindStringSubmatch(line); len(volNum) > 2 {
				currentEntry.Fields["volume"] = simpleBibString(volNum[1])
				currentEntry.Fields["number"] = simpleBibString(volNum[2])
			}
			if publisher := regexp.MustCompile(`([A-Z][a-z]+ (?:Press|Publications|Publishing|University|Inc\.))`).FindString(line); publisher != "" {
				currentEntry.Fields["publisher"] = simpleBibString(publisher)
			}
			if doi := regexp.MustCompile(`DOI:\s*([\w/.]+)`).FindStringSubmatch(line); len(doi) > 1 {
				currentEntry.Fields["doi"] = simpleBibString(doi[1])
			}
			if url := regexp.MustCompile(`\\url\{(.*?)\}`).FindStringSubmatch(line); len(url) > 1 {
				currentEntry.Fields["url"] = simpleBibString(url[1])
			}
			if _, hasArxivID := currentEntry.Fields["arxiv_id"]; !hasArxivID {
				if arxivID := regexp.MustCompile(`arXiv:(\d{4}\.\d{4,5})`).FindStringSubmatch(line); len(arxivID) > 1 {
					currentEntry.Fields["arxiv_id"] = simpleBibString(arxivID[0]) // [0] to get ful lstring arxiv:2345.2093, important for later parsing
				}
			}
		}

		// Check if this is the last line or if the next line starts a new entry
		if i == len(lines)-1 || strings.HasPrefix(lines[i+1], "\\bibitem") {
			if inEntry {
				entries = append(entries, currentEntry)
				inEntry = false
			}
		}
	}

	for _, entry := range entries {
		pl.logger.Info().Msgf("Processed entry: %v", entry)
	}

	return entries, nil
}

func (pl *PaperLoader) formatReferences(references []*bibtex.BibEntry) []map[string]interface{} {
	pl.logger.Info().Msg("Formatting references")

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

// New helper method to format existing references
func (pl *PaperLoader) formatExistingReferences(references []models.PaperReference) []map[string]interface{} {
	pl.logger.Info().Msg("Formatting existing references")

	var formattedReferences []map[string]interface{}
	for _, ref := range references {
		formattedReferences = append(formattedReferences, map[string]interface{}{
			"text":                  ref.FormattedText,
			"arxiv_id":              ref.ArxivID,
			"is_available_on_arxiv": ref.IsAvailableOnArxiv,
		})
	}
	return formattedReferences
}

func (pl *PaperLoader) formatReference(entry *bibtex.BibEntry) string {
	pl.logger.Info().Msg("Formatting reference")

	getField := func(key string, defaultValue string) string {
		if field, ok := entry.Fields[key]; ok && field != nil {
			return field.String()
		}
		return defaultValue
	}

	authors := getField("author", "Unknown Author")
	title := getField("title", "Untitled")
	year := getField("year", "n.d.")
	journal := getField("journal", "")
	if journal == "" {
		journal = getField("booktitle", "")
	}
	arxivID := getField("arxiv_id", "")
	if arxivID != "" {
		return fmt.Sprintf("%s. (%s). %s. %s [%s]", authors, year, title, journal, arxivID)
	}

	return fmt.Sprintf("%s. (%s). %s. %s", authors, year, title, journal)
}

func (pl *PaperLoader) detectArxivID(reference string) string {
	pl.logger.Info().Msg("Detecting ArxivID")

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

func (pl *PaperLoader) GetPaperMetadata(arxivID string) (map[string]string, error) {
	pl.logger.Info().Msgf("Fetching metadata for paper with ArxivID: %s", arxivID)

	url := fmt.Sprintf("http://export.arxiv.org/api/query?id_list=%s", arxivID)

	resp, err := http.Get(url)
	if err != nil {
		pl.logger.Error().Err(err).Msgf("Failed to fetch arXiv metadata for paper with ArxivID: %s", arxivID)
		return nil, fmt.Errorf("failed to fetch arXiv metadata: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		pl.logger.Error().Err(err).Msgf("Failed to read response body for paper with ArxivID: %s", arxivID)
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	var feed ArxivFeed
	err = xml.Unmarshal(body, &feed)
	if err != nil {
		pl.logger.Error().Err(err).Msgf("Failed to parse XML response for paper with ArxivID: %s", arxivID)
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

// GetPaperByArxivID retrieves a paper from the database by its ArxivID
func (pl *PaperLoader) GetPaperByArxivID(arxivID string) (*models.Paper, error) {
	pl.logger.Info().Msgf("Retrieving paper with ArxivID: %s", arxivID)

	var paper models.Paper
	result := database.DB.Where("arxiv_id = ?", arxivID).First(&paper)
	if result.Error != nil {
		pl.logger.Error().Err(result.Error).Msgf("Failed to retrieve paper with ArxivID: %s", arxivID)
		return nil, result.Error
	}
	return &paper, nil
}
