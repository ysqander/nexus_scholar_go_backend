package bibtexparser

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type BibEntry struct {
	Type     string
	CiteName string
	Fields   map[string]string
}

func ParseBibTeX(content string) ([]BibEntry, error) {
	// Write content to a temporary file
	tmpFile, err := os.CreateTemp("", "bibtex_*.bib")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	if _, err := tmpFile.WriteString(content); err != nil {
		return nil, fmt.Errorf("failed to write to temporary file: %v", err)
	}
	tmpFile.Close()

	// Run bibtool command with custom formatting
	cmd := exec.Command("bibtool",
		"-r", "-", // Read resource from stdin
		"-f", "{%T}\t{%s(key)}\t{%s(type)}\t{%s(title)}\t{%N}\t{%s(year)}\t{%s(journal)}\t{%s(volume)}\t{%s(number)}\t{%s(pages)}\t{%s(publisher)}\t{%s(doi)}\t{%s(url)}\t{%0n}\t{%s(arxiv)}\n",
		tmpFile.Name())

	// Provide resource configuration via stdin
	cmd.Stdin = strings.NewReader(`
		print.line.length = 1000000
		print.newline = "\n"
		print.deleted.prefix = ""
		print.deleted.postfix = ""
		print.use.tab = "on"
		print.align.key = 0
		new.format.type = "{%s}"
		new.entry.type = ""
		resource.type = "{%s}"
		fmt.et.al = ""
		fmt.name.name = "{%1n(author)}"
		fmt.title.title = "{%t(title)}"
		fmt.journal = "{%j}"
	`)

	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("bibtool command failed: %v", err)
	}

	return parseBibtoolOutput(out.String())
}

func parseBibtoolOutput(output string) ([]BibEntry, error) {
	var references []BibEntry
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) < 15 {
			continue // Skip incomplete entries
		}

		ref := BibEntry{
			Type:     fields[0],
			CiteName: fields[1],
			Fields: map[string]string{
				"title":     fields[3],
				"author":    fields[4],
				"year":      fields[5],
				"journal":   fields[6],
				"volume":    fields[7],
				"number":    fields[8],
				"pages":     fields[9],
				"publisher": fields[10],
				"doi":       fields[11],
				"url":       fields[12],
				"raw":       fields[13],
				"arxiv":     fields[14],
			},
		}

		references = append(references, ref)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading bibtool output: %v", err)
	}

	return references, nil
}
