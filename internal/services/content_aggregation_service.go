package services

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
		content, title, err := s.processArXivPaper(id)
		if err != nil {
			return "", fmt.Errorf("failed to process arXiv paper %s: %v", id, err)
		}
		aggregatedContent.WriteString(fmt.Sprintf("<Document>\n<title>Title:%s</title>\n%s\n</Document>\n", title, content))
	}

	// Process user-provided PDFs
	for _, pdfPath := range userPDFs {
		if pdfPath == "" {
			continue
		}
		content, title, err := s.ProcessUserPDF(pdfPath)
		if err != nil {
			return "", fmt.Errorf("failed to process PDF %s: %v", pdfPath, err)
		}
		aggregatedContent.WriteString(fmt.Sprintf("<Document>\n<title>Title:%s</title>\n%s\n</Document>\n", title, content))
	}

	return aggregatedContent.String(), nil
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
