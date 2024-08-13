package services

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ContentAggregationService handles the aggregation of content from various sources
type ContentAggregationService struct {
	arxivBaseURL string
}

func NewContentAggregationService(arxivBaseURL string) *ContentAggregationService {
	return &ContentAggregationService{
		arxivBaseURL: arxivBaseURL,
	}
}

func (s *ContentAggregationService) AggregateDocuments(arxivIDs []string, userPDFs []string) (string, error) {
	var aggregatedContent strings.Builder
	var summary strings.Builder
	documentCount := 0

	// Process arXiv papers
	for _, id := range arxivIDs {
		documentCount++
		content, title, err := s.processArXivPaper(id)
		if err != nil {
			return "", fmt.Errorf("failed to process arXiv paper %s: %v", id, err)
		}
		documentString := fmt.Sprintf("<Document %d>\n<title>Title: %s</title>\n%s\n</Document %d>\n", documentCount, title, content, documentCount)
		aggregatedContent.WriteString(documentString)
		summary.WriteString(fmt.Sprintf("Document %d: %s (arXiv ID: %s)\n", documentCount, title, id))
	}

	// Process user-provided PDFs
	for _, pdfPath := range userPDFs {
		if pdfPath == "" {
			continue
		}
		documentCount++
		content, title, err := s.ProcessUserPDF(pdfPath)
		if err != nil {
			return "", fmt.Errorf("failed to process PDF %s: %v", pdfPath, err)
		}
		documentString := fmt.Sprintf("<Document %d>\n<title>Title: %s</title>\n%s\n</Document%d>\n", documentCount, title, content, documentCount)
		aggregatedContent.WriteString(documentString)
		summary.WriteString(fmt.Sprintf("Document %d: %s (User PDF)\n", documentCount, title))
	}

	// Prepend the summary to the aggregated content
	finalContent := fmt.Sprintf("Summary of Documents:\n%s\n\nAggregated Content:\n%s", summary.String(), aggregatedContent.String())

	return finalContent, nil
}

func (s *ContentAggregationService) processArXivPaper(arxivID string) (string, string, error) {
	// Fetch metadata from the database
	paperMetadata, err := GetReferenceByArxivID(arxivID)
	if err != nil {
		return "", "", fmt.Errorf("failed to fetch metadata from the database: %v", err)
	}

	// Download the PDF from arXiv
	pdfURL := fmt.Sprintf("%s%s.pdf", s.arxivBaseURL, arxivID)
	resp, err := http.Get(pdfURL)
	if err != nil {
		return "", "", fmt.Errorf("failed to download arXiv paper: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("unexpected status code when downloading arXiv paper: %d", resp.StatusCode)
	}

	// Create a temporary file to store the PDF
	tempFile, err := os.CreateTemp("", "arxiv-*.pdf")
	if err != nil {
		return "", "", fmt.Errorf("failed to create temporary file: %v", err)
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	// Save the PDF content to the temporary file
	_, err = io.Copy(tempFile, resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("failed to save PDF content: %v", err)
	}

	// Process the PDF file
	content, err := s.ExtractTextFromPDF(tempFile.Name())
	if err != nil {
		return "", "", fmt.Errorf("failed to extract text from PDF: %v", err)
	}

	return content, paperMetadata.Title, nil
}

func (s *ContentAggregationService) ProcessUserPDF(pdfPath string) (string, string, error) {
	// Try to extract metadata first
	// title, err := s.extractPDFMetadata(pdfPath)
	// if err != nil {
	// 	// Log the error, but continue processing
	// 	log.Printf("Warning: Failed to extract PDF metadata: %v", err)
	// }

	// Extract content
	content, err := s.ExtractTextFromPDF(pdfPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to extract text from PDF: %v", err)
	}

	// // If no title was found in metadata, use filename as fallback
	// if title == "" {
	// 	title = filepath.Base(pdfPath)
	// }
	title := filepath.Base(pdfPath)
	title = strings.TrimSuffix(title, ".pdf")

	return content, title, nil
}

// func (s *ContentAggregationService) extractPDFMetadata(pdfPath string) (string, error) {
// 	// Use the pdfcpu API to extract metadata
// 	ctx, err := api.ReadContextFile(pdfPath)
// 	if err != nil {
// 		return "", fmt.Errorf("failed to read PDF file: %v", err)
// 	}

// 	// Extract metadata from the context
// 	if ctx.XRefTable != nil && ctx.XRefTable.Info != nil {
// 		infoDict, err := ctx.DereferenceDict(*ctx.XRefTable.Info)
// 		if err != nil {
// 			return "", fmt.Errorf("failed to dereference Info dictionary: %v", err)
// 		}
// 		if title, found := infoDict.Find("Title"); found {
// 			if titleStr, ok := title.(types.StringLiteral); ok {
// 				return string(titleStr), nil
// 			}
// 		}
// 	}

// 	return "", nil
// }

func (s *ContentAggregationService) ExtractTextFromPDF(pdfPath string) (string, error) {
	// Check if pdftotext is installed
	_, err := exec.LookPath("pdftotext")
	if err != nil {
		return "", fmt.Errorf("pdftotext is not installed: %v", err)
	}

	// Run pdftotext command
	cmd := exec.Command("pdftotext",
		"-layout",      // Maintains basic layout
		"-nopgbrk",     // Removes page breaks
		"-eol", "unix", // Consistent line endings
		"-enc", "UTF-8", // Ensures proper character encoding
		"-q",    // Quiet mode, suppresses errors
		pdfPath, // Input PDF file
		"-",     // Output to stdout
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	err = cmd.Run()
	if err != nil {
		return "", fmt.Errorf("failed to run pdftotext: %v", err)
	}

	content := out.String()
	if content == "" {
		return "", fmt.Errorf("no text content extracted from PDF")
	}

	return content, nil
}
