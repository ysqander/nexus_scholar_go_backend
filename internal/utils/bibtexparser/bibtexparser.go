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
	tmpFile, err := os.CreateTemp("", "bibtex_*.bib")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(content); err != nil {
		return nil, fmt.Errorf("failed to write to temporary file: %v", err)
	}
	tmpFile.Close()

	formattedCmd := exec.Command("bibtool",
		"-r", "-",
		"-f", "{%T}\t{%K}\t{%s(type)}\t{%s(title)}\t{%N}\t{%s(year)}\t{%s(journal)}\t{%s(volume)}\t{%s(number)}\t{%s(pages)}\t{%s(publisher)}\t{%s(doi)}\t{%s(url)}\t{%0n}\t{%s(arxiv)}\n",
		tmpFile.Name())

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
		return nil, fmt.Errorf("bibtool command failed: %v", err)
	}

	formattedOutput := out.String()

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

	return parseBibtoolOutput(formattedOutput, rawOutput)
}

func parseBibtoolOutput(formattedOutput, rawOutput string) ([]BibEntry, error) {
	citeKeys := extractCiteKeys(rawOutput)

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

			entry := parseEntry(entryType, citeName, entryContent, rawEntry)
			if entry != nil {
				references = append(references, *entry)
			}
		} else {
			break
		}
	}

	if len(matches) != len(citeKeys) {
		return nil, fmt.Errorf("mismatch between cite keys (%d) and entries (%d)", len(citeKeys), len(matches))
	}

	return references, nil
}

func extractCiteKeys(rawOutput string) []string {
	var citeKeys []string
	pattern := regexp.MustCompile(`@(\w+)\s*{\s*([^,\s]+),`)

	matches := pattern.FindAllStringSubmatch(rawOutput, -1)
	for _, match := range matches {
		if len(match) == 3 {
			citeName := match[2]
			citeKeys = append(citeKeys, citeName)
		}
	}

	return citeKeys
}

func parseEntry(entryType, citeName, entryContent, rawEntry string) *BibEntry {
	entry := &BibEntry{
		Type:     entryType,
		CiteName: citeName,
		Fields:   make(map[string]string),
		RawEntry: rawEntry,
	}

	fields := strings.Split(entryContent, ",\n")

	var currentKey string
	var currentValue strings.Builder

	for _, field := range fields {
		if strings.Contains(field, "=") {
			if currentKey != "" {
				entry.Fields[currentKey] = strings.TrimSpace(currentValue.String())
				currentValue.Reset()
			}

			parts := strings.SplitN(field, "=", 2)
			currentKey = strings.TrimSpace(parts[0])
			currentValue.WriteString(strings.TrimSpace(parts[1]))
		} else {
			currentValue.WriteString(" ")
			currentValue.WriteString(strings.TrimSpace(field))
		}
	}

	if currentKey != "" {
		entry.Fields[currentKey] = strings.TrimSpace(currentValue.String())
	}

	for key, value := range entry.Fields {
		entry.Fields[key] = cleanFieldValue(value)
	}

	entry.Fields["arxiv"] = extractArXivID(entry.Fields)

	if author, ok := entry.Fields["author"]; ok {
		entry.Fields["author"] = cleanAuthorField(author)
	}

	return entry
}

func cleanFieldValue(value string) string {
	value = strings.Trim(value, "{}\"")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\t", " ")
	value = regexp.MustCompile(`\s+`).ReplaceAllString(value, " ")
	return strings.TrimSpace(value)
}

func extractArXivID(fields map[string]string) string {
	arXivPattern := regexp.MustCompile(`arXiv:(\d{4}\.\d{4,5}|[a-zA-Z\-]+/\d{7})`)

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
	author = strings.ReplaceAll(author, "{", "")
	author = strings.ReplaceAll(author, "}", "")
	author = strings.TrimSpace(author)

	if len(author) > 300 {
		// Find the last comma or space before the 300th character
		lastCommaIndex := strings.LastIndex(author[:300], ",")
		lastSpaceIndex := strings.LastIndex(author[:300], " ")
		truncateIndex := lastCommaIndex
		if lastSpaceIndex > lastCommaIndex {
			truncateIndex = lastSpaceIndex
		}
		if truncateIndex != -1 {
			author = author[:truncateIndex] + " et al."
		} else {
			// If no comma or space found, just truncate at 300 characters
			author = author[:300] + "..."
		}
	}
	return author
}
