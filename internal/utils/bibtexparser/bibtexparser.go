package bibtexparser

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/rs/zerolog"
)

type BibEntry struct {
	Type     string
	CiteName string
	Fields   map[string]string
	RawEntry string
}

func ParseBibTeX(content string, logger zerolog.Logger) ([]BibEntry, error) {
	// Write content to a temporary file
	tmpFile, err := os.CreateTemp("", "bibtex_*.bib")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	logger.Debug().Str("file", tmpFile.Name()).Msg("Created temporary file")

	if _, err := tmpFile.WriteString(content); err != nil {
		return nil, fmt.Errorf("failed to write to temporary file: %v", err)
	}
	tmpFile.Close()

	logger.Debug().Msg("Wrote content to temporary file")

	// Run bibtool command with custom formatting
	formattedCmd := exec.Command("bibtool",
		"-r", "-", // Read resource from stdin
		"-f", "{%T}\t{%K}\t{%s(type)}\t{%s(title)}\t{%N}\t{%s(year)}\t{%s(journal)}\t{%s(volume)}\t{%s(number)}\t{%s(pages)}\t{%s(publisher)}\t{%s(doi)}\t{%s(url)}\t{%0n}\t{%s(arxiv)}\n",
		tmpFile.Name())

	logger.Debug().Strs("args", formattedCmd.Args).Msg("Prepared bibtool command")

	// Provide resource configuration via stdin
	formattedCmd.Stdin = strings.NewReader(`
    print.line.length = 1000000
    print.deleted.prefix = ""
    print.deleted.postfix = ""
    print.use.tab = "on"
    print.align.key = 0
    fmt.et.al = ""
`)

	var out bytes.Buffer
	var stderr bytes.Buffer
	formattedCmd.Stdout = &out
	formattedCmd.Stderr = &stderr

	if err := formattedCmd.Run(); err != nil {
		logger.Error().Str("stderr", stderr.String()).Err(err).Msg("bibtool command failed")
		return nil, fmt.Errorf("bibtool command failed: %v", err)
	}

	logger.Debug().Msg("bibtool command executed successfully")

	// Log a partial chunk of the raw output
	formattedOutput := out.String()
	logPartialOutput(formattedOutput, logger, "Formatted bibtool output")

	// Second call to bibtool without formatting
	rawCmd := exec.Command("bibtool", "-r", "-", tmpFile.Name())
	rawCmd.Stdin = strings.NewReader(`
    print.line.length = 1000000
    print.deleted.prefix = ""
    print.deleted.postfix = ""
    print.use.tab = "on"
    print.align.key = 0
    fmt.et.al = ""
`)

	var rawOut bytes.Buffer
	var rawStderr bytes.Buffer
	rawCmd.Stdout = &rawOut
	rawCmd.Stderr = &rawStderr

	if err := rawCmd.Run(); err != nil {
		logger.Error().Str("stderr", rawStderr.String()).Err(err).Msg("raw bibtool command failed")
		return nil, fmt.Errorf("raw bibtool command failed: %v", err)
	}

	rawOutput := rawOut.String()
	logPartialOutput(rawOutput, logger, "Raw bibtool output")

	return parseBibtoolOutput(formattedOutput, rawOutput, logger)
}

func parseBibtoolOutput(formattedOutput, rawOutput string, logger zerolog.Logger) ([]BibEntry, error) {
	// Extract citation keys from raw output
	citeKeys := extractCiteKeys(rawOutput, logger)

	var references []BibEntry
	entryPattern := regexp.MustCompile(`(?m)^\n@(\w+)\{([^,]*),\n((?:.|\n)*?)\n\}\n`)

	matches := entryPattern.FindAllStringSubmatch(formattedOutput, -1)
	rawMatches := entryPattern.FindAllStringSubmatch(rawOutput, -1)

	for i, match := range matches {
		if i < len(citeKeys) {
			entryType := strings.ToLower(match[1])
			citeName := citeKeys[i]
			entryContent := match[3]
			rawEntry := rawMatches[i][0]

			// Debug log the first five entryContent
			if i < 5 {
				logger.Debug().
					Str("entryType", entryType).
					Str("citeName", citeName).
					Str("entryContent", entryContent).
					Msgf("Entry content %d", i+1)
			}

			entry := parseEntry(entryType, citeName, entryContent, rawEntry, logger)
			if entry != nil {
				references = append(references, *entry)

				// Log the first 5 post-parsing entries
				if i < 5 {
					logger.Debug().
						Str("entryType", entry.Type).
						Str("citeName", entry.CiteName).
						Int("fieldCount", len(entry.Fields)).
						Msg(fmt.Sprintf("Parsed entry %d", i+1))
				}
			}
		} else {
			logger.Warn().Msg("More entries than cite keys")
			break
		}
	}

	if len(matches) != len(citeKeys) {
		logger.Error().
			Int("citeKeyCount", len(citeKeys)).
			Int("entryCount", len(matches)).
			Msg("Mismatch between cite keys and entries")
		return nil, fmt.Errorf("mismatch between cite keys (%d) and entries (%d)", len(citeKeys), len(matches))
	}

	logger.Info().Int("count", len(references)).Msg("Parsed references")
	return references, nil
}

func extractCiteKeys(rawOutput string, logger zerolog.Logger) []string {
	var citeKeys []string
	pattern := regexp.MustCompile(`@(\w+)\s*{\s*([^,\s]+),`)

	matches := pattern.FindAllStringSubmatch(rawOutput, -1)
	for _, match := range matches {
		if len(match) == 3 {
			citeName := match[2]
			citeKeys = append(citeKeys, citeName)
		}
	}

	logger.Debug().Int("citeKeyCount", len(citeKeys)).Msg("Extracted cite keys")
	return citeKeys
}

func parseEntry(entryType, citeName, entryContent, rawEntry string, logger zerolog.Logger) *BibEntry {
	entry := &BibEntry{
		Type:     entryType,
		CiteName: citeName,
		Fields:   make(map[string]string),
		RawEntry: rawEntry,
	}

	// Split the content by field, not by line
	fields := strings.Split(entryContent, ",\n")

	var currentKey string
	var currentValue strings.Builder

	for _, field := range fields {
		if strings.Contains(field, "=") {
			// If we have a previous field, add it to the entry
			if currentKey != "" {
				entry.Fields[currentKey] = strings.TrimSpace(currentValue.String())
				currentValue.Reset()
			}

			// Start a new field
			parts := strings.SplitN(field, "=", 2)
			currentKey = strings.TrimSpace(parts[0])
			currentValue.WriteString(strings.TrimSpace(parts[1]))
		} else {
			// Continue the previous field
			currentValue.WriteString(" ")
			currentValue.WriteString(strings.TrimSpace(field))
		}
	}

	// Add the last field
	if currentKey != "" {
		entry.Fields[currentKey] = strings.TrimSpace(currentValue.String())
	}

	// Clean up field values
	for key, value := range entry.Fields {
		entry.Fields[key] = cleanFieldValue(value)
	}

	// Extract arXiv ID
	entry.Fields["arxiv"] = extractArXivID(entry.Fields)

	// Clean up author field
	if author, ok := entry.Fields["author"]; ok {
		entry.Fields["author"] = cleanAuthorField(author)
	}

	logger.Debug().
		Str("type", entry.Type).
		Str("citeName", entry.CiteName).
		Int("fieldCount", len(entry.Fields)).
		Msg("Parsed entry")

	return entry
}

func cleanFieldValue(value string) string {
	// Remove surrounding braces and quotes
	value = strings.Trim(value, "{}\"")
	// Replace newlines and tabs with spaces
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\t", " ")
	// Collapse multiple spaces into one
	value = regexp.MustCompile(`\s+`).ReplaceAllString(value, " ")
	return strings.TrimSpace(value)
}

func extractArXivID(fields map[string]string) string {
	arXivPattern := regexp.MustCompile(`arXiv:(\d{4}\.\d{4,5}|[a-zA-Z\-]+/\d{7})`)

	// Check common fields for arXiv ID
	for _, field := range []string{"journal", "title", "url", "doi", "arxiv"} {
		if value, ok := fields[field]; ok {
			if matches := arXivPattern.FindStringSubmatch(value); matches != nil {
				return matches[1]
			}
		}
	}

	return ""
}

func cleanAuthorField(author string) string {
	// Remove any remaining curly braces and trim spaces
	author = strings.ReplaceAll(author, "{", "")
	author = strings.ReplaceAll(author, "}", "")
	return strings.TrimSpace(author)
}

// Update logPartialOutput to include a message parameter
func logPartialOutput(output string, logger zerolog.Logger, message string) {
	const maxChars = 1500
	if len(output) > maxChars {
		logger.Debug().Str("partial_output", output[:maxChars]+"...").Msg(message)
	} else {
		logger.Debug().Str("output", output).Msg(message)
	}
}
