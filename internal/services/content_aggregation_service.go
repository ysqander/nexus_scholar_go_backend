package services

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/ledongthuc/pdf"
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

	// Process arXiv papers
	for _, id := range arxivIDs {
		content, err := s.processArXivPaper(id)
		if err != nil {
			return "", fmt.Errorf("failed to process arXiv paper %s: %v", id, err)
		}
		aggregatedContent.WriteString(fmt.Sprintf("<Document>\n<title>arXiv:%s</title>\n%s\n</Document>\n", id, content))
	}

	// Process user-provided PDFs
	for i, pdfPath := range userPDFs {
		if pdfPath == "" {
			continue
		}
		content, err := s.processUserPDF(pdfPath)
		if err != nil {
			return "", fmt.Errorf("failed to process PDF %s: %v", pdfPath, err)
		}
		aggregatedContent.WriteString(fmt.Sprintf("<Document>\n<title>User PDF %d</title>\n%s\n</Document>\n", i+1, content))
	}

	return aggregatedContent.String(), nil
}

func (s *ContentAggregationService) processArXivPaper(arxivID string) (string, error) {
	// Download the PDF from arXiv
	pdfURL := fmt.Sprintf("%s%s.pdf", s.arxivBaseURL, arxivID)
	resp, err := http.Get(pdfURL)
	if err != nil {
		return "", fmt.Errorf("failed to download arXiv paper: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code when downloading arXiv paper: %d", resp.StatusCode)
	}

	// Create a temporary file to store the PDF
	tempFile, err := os.CreateTemp("", "arxiv-*.pdf")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary file: %v", err)
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	// Save the PDF content to the temporary file
	_, err = io.Copy(tempFile, resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to save PDF content: %v", err)
	}

	// Process the PDF file
	content, err := s.extractTextFromPDF(tempFile.Name())
	if err != nil {
		return "", fmt.Errorf("failed to extract text from PDF: %v", err)
	}

	return content, nil
}

func (s *ContentAggregationService) processUserPDF(pdfPath string) (string, error) {
	return s.extractTextFromPDF(pdfPath)
}

func (s *ContentAggregationService) extractTextFromPDF(pdfPath string) (string, error) {
	f, r, err := pdf.Open(pdfPath)
	if err != nil {
		return "", fmt.Errorf("failed to open PDF: %v", err)
	}
	defer f.Close()

	var content strings.Builder
	totalPage := r.NumPage()

	for pageIndex := 1; pageIndex <= totalPage; pageIndex++ {
		p := r.Page(pageIndex)
		if p.V.IsNull() {
			continue
		}

		text, err := p.GetPlainText(nil)
		if err != nil {
			continue
		}
		content.WriteString(text)
		content.WriteString("\n\n") // Add separation between pages
	}

	if content.Len() == 0 {
		return "", fmt.Errorf("no text content extracted from PDF")
	}

	return content.String(), nil
}
