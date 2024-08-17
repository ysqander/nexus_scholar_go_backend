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

	"github.com/rs/zerolog"
)

// ContentAggregationService handles the aggregation of content from various sources
type ContentAggregationService struct {
	arxivBaseURL string
	logger       zerolog.Logger
}

func NewContentAggregationService(arxivBaseURL string, logger zerolog.Logger) *ContentAggregationService {
	return &ContentAggregationService{
		arxivBaseURL: arxivBaseURL,
		logger:       logger,
	}
}

func (s *ContentAggregationService) AggregateDocuments(arxivIDs []string, userPDFs []string) (string, error) {
	s.logger.Info().Msg("Starting to aggregate documents")
	var aggregatedContent strings.Builder
	var summary strings.Builder
	documentCount := 0

	// Process arXiv papers
	for _, id := range arxivIDs {
		s.logger.Info().Msgf("Processing arXiv paper with ID: %s", id)
		documentCount++
		content, title, err := s.processArXivPaper(id)
		if err != nil {
			s.logger.Error().Err(err).Msgf("Failed to process arXiv paper with ID: %s", id)
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
		s.logger.Info().Msgf("Processing user PDF with path: %s", pdfPath)
		documentCount++
		content, title, err := s.ProcessUserPDF(pdfPath)
		if err != nil {
			s.logger.Error().Err(err).Msgf("Failed to process user PDF with path: %s", pdfPath)
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
	s.logger.Info().Msgf("Processing arXiv paper with ID: %s", arxivID)
	// Fetch metadata from the database
	paperMetadata, err := GetReferenceByArxivID(arxivID)
	if err != nil {
		s.logger.Error().Err(err).Msgf("Failed to fetch metadata for arXiv paper with ID: %s", arxivID)
		return "", "", fmt.Errorf("failed to fetch metadata from the database: %v", err)
	}

	// Download the PDF from arXiv
	pdfURL := fmt.Sprintf("%s%s.pdf", s.arxivBaseURL, arxivID)
	resp, err := http.Get(pdfURL)
	if err != nil {
		s.logger.Error().Err(err).Msgf("Failed to download arXiv paper with ID: %s", arxivID)
		return "", "", fmt.Errorf("failed to download arXiv paper: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		s.logger.Error().Msgf("Unexpected status code when downloading arXiv paper with ID: %s. Status code: %d", arxivID, resp.StatusCode)
		return "", "", fmt.Errorf("unexpected status code when downloading arXiv paper: %d", resp.StatusCode)
	}

	// Create a temporary file to store the PDF
	tempFile, err := os.CreateTemp("", "arxiv-*.pdf")
	if err != nil {
		s.logger.Error().Err(err).Msgf("Failed to create temporary file for arXiv paper with ID: %s", arxivID)
		return "", "", fmt.Errorf("failed to create temporary file: %v", err)
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	// Save the PDF content to the temporary file
	_, err = io.Copy(tempFile, resp.Body)
	if err != nil {
		s.logger.Error().Err(err).Msgf("Failed to save PDF content for arXiv paper with ID: %s", arxivID)
		return "", "", fmt.Errorf("failed to save PDF content: %v", err)
	}

	// Process the PDF file
	content, err := s.ExtractTextFromPDF(tempFile.Name())
	if err != nil {
		s.logger.Error().Err(err).Msgf("Failed to extract text from PDF for arXiv paper with ID: %s", arxivID)
		return "", "", fmt.Errorf("failed to extract text from PDF: %v", err)
	}

	return content, paperMetadata.Title, nil
}

func (s *ContentAggregationService) ProcessUserPDF(pdfPath string) (string, string, error) {
	s.logger.Info().Msgf("Processing user PDF with path: %s", pdfPath)

	// Extract content
	content, err := s.ExtractTextFromPDF(pdfPath)
	if err != nil {
		s.logger.Error().Err(err).Msgf("Failed to extract text from PDF for path: %s", pdfPath)
		return "", "", fmt.Errorf("failed to extract text from PDF: %v", err)
	}

	title := filepath.Base(pdfPath)
	title = strings.TrimSuffix(title, ".pdf")

	return content, title, nil
}

func (s *ContentAggregationService) ExtractTextFromPDF(pdfPath string) (string, error) {
	s.logger.Info().Msgf("Extracting text from PDF with path: %s", pdfPath)
	// Check if pdftotext is installed
	_, err := exec.LookPath("pdftotext")
	if err != nil {
		s.logger.Error().Err(err).Msg("pdftotext is not installed")
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
		s.logger.Error().Err(err).Msgf("Failed to run pdftotext for PDF with path: %s", pdfPath)
		return "", fmt.Errorf("failed to run pdftotext: %v", err)
	}

	content := out.String()
	if content == "" {
		s.logger.Error().Msgf("No text content extracted from PDF with path: %s", pdfPath)
		return "", fmt.Errorf("no text content extracted from PDF")
	}

	return content, nil
}
