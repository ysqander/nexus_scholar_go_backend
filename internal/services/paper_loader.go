package services

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"nexus_scholar_go_backend/internal/database"
	"nexus_scholar_go_backend/internal/models"
	"nexus_scholar_go_backend/internal/utils/bibtexparser"
	"nexus_scholar_go_backend/internal/utils/errors"
	"os"
	"regexp"
	"strings"

	"encoding/xml"

	"github.com/rs/zerolog"
)

type PaperLoader struct {
	logger zerolog.Logger
}

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

	var references []bibtexparser.BibEntry
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

	pl.logger.Debug().Msgf("First 5 references before formatting: %+v", references[:min(5, len(references))])
	formattedReferences := pl.formatReferences(references)
	pl.logger.Debug().Msgf("First 5 formatted references: %+v", formattedReferences[:min(5, len(formattedReferences))])

	// Now create and save references to the database
	for i, formattedRef := range formattedReferences {
		dbRef := models.PaperReference{
			ArxivID:            formattedRef["arxiv_id"].(string),
			ParentArxivID:      arxivID,
			Type:               references[i].Type,
			Key:                references[i].CiteName,
			Title:              references[i].Fields["title"],
			Author:             references[i].Fields["author"],
			Year:               references[i].Fields["year"],
			Journal:            references[i].Fields["journal"],
			Volume:             references[i].Fields["volume"],
			Number:             references[i].Fields["number"],
			Pages:              references[i].Fields["pages"],
			Publisher:          references[i].Fields["publisher"],
			DOI:                references[i].Fields["doi"],
			URL:                references[i].Fields["url"],
			RawBibEntry:        references[i].RawEntry,
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

func (pl *PaperLoader) parseBibFiles(bibFiles []string) ([]bibtexparser.BibEntry, error) {
	pl.logger.Info().Msg("Starting to parse .bib files")

	var allReferences []bibtexparser.BibEntry
	for i, content := range bibFiles {
		pl.logger.Debug().Msgf("Parsing .bib file %d of %d", i+1, len(bibFiles))
		pl.logger.Debug().Msgf("Content of .bib file %d: %s", i+1, content[:min(500, len(content))]) // Log the first 500 characters of the content

		entries, err := bibtexparser.ParseBibTeX(content, pl.logger)
		if err != nil {
			pl.logger.Error().Err(err).Msgf("Failed to parse .bib file %d", i+1)
			return nil, fmt.Errorf("failed to parse .bib file %d: %v", i+1, err)
		}

		// Process multi-line fields
		for i := range entries {
			for key, value := range entries[i].Fields {
				// Preserve newlines in the title field
				if key == "title" {
					entries[i].Fields[key] = strings.TrimSpace(value)
				} else {
					entries[i].Fields[key] = strings.TrimSpace(strings.Replace(value, "\n", " ", -1))
				}
			}
		}

		pl.logger.Debug().Msgf("Successfully parsed .bib file %d. Found %d entries", i+1, len(entries))
		allReferences = append(allReferences, entries...)
	}

	pl.logger.Info().Msgf("Finished parsing all .bib files. Total references found: %d", len(allReferences))

	if len(allReferences) == 0 {
		pl.logger.Warn().Msg("No references found in any of the .bib files")
		return nil, errors.New404Error("No references found in the provided .bib files")
	}

	return allReferences, nil
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

func (pl *PaperLoader) parseBBLFile(content string) ([]bibtexparser.BibEntry, error) {
	var entries []bibtexparser.BibEntry
	lines := strings.Split(content, "\n")

	var currentEntry *bibtexparser.BibEntry
	var authorLines []string
	var inEntry bool
	var inAuthor bool
	var titleLines []string
	var inTitle bool

	for i, line := range lines {
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "\\bibitem") {
			if inEntry {
				entries = append(entries, *currentEntry)
			}
			currentEntry = &bibtexparser.BibEntry{Fields: make(map[string]string)}
			currentEntry.CiteName = strings.Trim(strings.TrimPrefix(line, "\\bibitem"), "{}")
			inEntry = true
			inAuthor = true
			authorLines = []string{}
			inTitle = false
			titleLines = []string{}
			continue
		}

		if inEntry {
			if strings.HasPrefix(line, "\\newblock") {
				if inAuthor {
					currentEntry.Fields["author"] = strings.Join(authorLines, " ")
					inAuthor = false
					inTitle = true
				} else if inTitle {
					currentEntry.Fields["title"] = strings.Trim(strings.Join(titleLines, " "), ".")
					inTitle = false
				}

				line = strings.TrimPrefix(line, "\\newblock")

				if match := regexp.MustCompile(`\\em\s*(.*?)\s*`).FindStringSubmatch(line); len(match) > 1 {
					emContent := strings.Trim(match[1], "{},")
					if strings.Contains(emContent, "arXiv") {
						currentEntry.Fields["journal"] = emContent
						if arxivMatch := regexp.MustCompile(`arXiv:(\d{4}\.\d{4,5})`).FindStringSubmatch(emContent); len(arxivMatch) > 1 {
							currentEntry.Fields["arxiv_id"] = arxivMatch[0] //[0] to get ful lstring arxiv:2345.2093, important for later parsing
						}
					} else {
						currentEntry.Fields["journal"] = emContent
					}
				} else if inTitle {
					titleLines = append(titleLines, strings.Trim(line, "."))
				}
			} else if inAuthor {
				authorLines = append(authorLines, line)
			} else if inTitle {
				titleLines = append(titleLines, line)
			}

			// Extract other details
			if year := regexp.MustCompile(`\b(\d{4})\b`).FindString(line); year != "" {
				currentEntry.Fields["year"] = year
			}
			if pages := regexp.MustCompile(`pages?\s*(\d+--?\d*)`).FindStringSubmatch(line); len(pages) > 1 {
				currentEntry.Fields["pages"] = pages[1]
			}
			if volNum := regexp.MustCompile(`(\d+)\s*\((\d+)\)`).FindStringSubmatch(line); len(volNum) > 2 {
				currentEntry.Fields["volume"] = volNum[1]
				currentEntry.Fields["number"] = volNum[2]
			}
			if publisher := regexp.MustCompile(`([A-Z][a-z]+ (?:Press|Publications|Publishing|University|Inc\.))`).FindString(line); publisher != "" {
				currentEntry.Fields["publisher"] = publisher
			}
			if doi := regexp.MustCompile(`DOI:\s*([\w/.]+)`).FindStringSubmatch(line); len(doi) > 1 {
				currentEntry.Fields["doi"] = doi[1]
			}
			if url := regexp.MustCompile(`\\url\{(.*?)\}`).FindStringSubmatch(line); len(url) > 1 {
				currentEntry.Fields["url"] = url[1]
			}
			if _, hasArxivID := currentEntry.Fields["arxiv_id"]; !hasArxivID {
				if arxivID := regexp.MustCompile(`arXiv:(\d{4}\.\d{4,5})`).FindStringSubmatch(line); len(arxivID) > 1 {
					currentEntry.Fields["arxiv_id"] = arxivID[0] // [0] to get ful lstring arxiv:2345.2093, important for later parsing
				}
			}
		}

		// Check if this is the last line or if the next line starts a new entry
		if i == len(lines)-1 || strings.HasPrefix(lines[i+1], "\\bibitem") {
			if inEntry {
				entries = append(entries, *currentEntry)
				inEntry = false
			}
		}
	}

	for _, entry := range entries {
		pl.logger.Info().Msgf("Processed entry: %v", entry)
	}

	return entries, nil
}

func (pl *PaperLoader) formatReferences(references []bibtexparser.BibEntry) []map[string]interface{} {
	pl.logger.Info().Msg("Formatting references and removing duplicates")

	// Create a map to store unique references
	uniqueReferences := make(map[string]map[string]interface{})

	for i, ref := range references {

		formattedRef := pl.formatReference(&ref)

		detectedArxivID := pl.detectArxivID(formattedRef)

		formattedReference := map[string]interface{}{
			"text":                  formattedRef,
			"arxiv_id":              detectedArxivID,
			"is_available_on_arxiv": detectedArxivID != "",
		}

		// If the arxiv_id is not empty, use it as the key; otherwise, use the formatted text
		key := detectedArxivID
		if key == "" {
			key = formattedRef
		}

		// Only add the reference if it's not already in the map
		if _, exists := uniqueReferences[key]; !exists {
			uniqueReferences[key] = formattedReference
		}

		// Debug logging for a sample of 5 entries
		if i < 5 {
			pl.logger.Debug().Msgf("Sample reference %d:", i+1)
			pl.logger.Debug().Msgf("  Original: %+v", ref)
			pl.logger.Debug().Msgf("  Formatted: %+v", formattedReference)
		}
	}

	// Convert the map back to a slice
	var formattedReferences []map[string]interface{}
	for _, ref := range uniqueReferences {
		formattedReferences = append(formattedReferences, ref)
	}

	pl.logger.Info().Msgf("Total unique formatted references: %d", len(formattedReferences))
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

func (pl *PaperLoader) formatReference(entry *bibtexparser.BibEntry) string {
	pl.logger.Info().Msg("Formatting reference")

	getField := func(key string, defaultValue string) string {
		if field, ok := entry.Fields[key]; ok {
			// Remove any surrounding braces and trim spaces
			return strings.Trim(strings.TrimSpace(field), "{}")
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
	if arxivID == "" {
		arxivID = getField("eprint", "")
	}

	if arxivID != "" {
		// Check if arxivID already starts with "arXiv:" prefix
		if !strings.HasPrefix(strings.ToLower(arxivID), "arxiv:") {
			arxivID = "arXiv:" + arxivID
		}
		return fmt.Sprintf("%s. (%s). %s. %s [%s]", authors, year, title, journal, arxivID)
	}

	return fmt.Sprintf("%s. (%s). %s. %s", authors, year, title, journal)
}

func (pl *PaperLoader) detectArxivID(reference string) string {
	pl.logger.Info().Msg("Detecting ArxivID")

	arxivPattern := `(?i)(?:arxiv:|https?://arxiv.org/abs/|arXiv:)(\d{4}\.\d{4,5})`
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

// TestBibParsing downloads and parses bib files for a given arXiv ID
func (pl *PaperLoader) TestBibParsing(arxivID string) error {
	// Download the paper source
	sourceContent, err := pl.downloadPaper(arxivID)
	if err != nil {
		return fmt.Errorf("failed to download paper: %v", err)
	}

	// Extract bib files
	bibFiles, err := pl.extractBibFiles(sourceContent)
	if err != nil {
		return fmt.Errorf("failed to extract bib files: %v", err)
	}

	// Parse bib files
	references, err := pl.parseBibFiles(bibFiles)
	if err != nil {
		return fmt.Errorf("failed to parse bib files: %v", err)
	}

	// Save output to a file
	outputFile := fmt.Sprintf("TEST_%s_parsed_references.json", arxivID)
	file, err := os.Create(outputFile)
	if err != nil {
		return fmt.Errorf("failed to create output file: %v", err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(references); err != nil {
		return fmt.Errorf("failed to write references to file: %v", err)
	}

	pl.logger.Info().Msgf("Parsed references saved to %s", outputFile)
	return nil
}
