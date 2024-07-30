package services

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jung-kurt/gofpdf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockPDFProcessor is a mock for PDF processing
type MockPDFProcessor struct {
	mock.Mock
}

func (m *MockPDFProcessor) extractTextFromPDF(reader io.Reader) (string, error) {
	args := m.Called(reader)
	return args.String(0), args.Error(1)
}

func createTestPDF(content string) ([]byte, error) {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	pdf.SetFont("Arial", "", 12)
	pdf.Cell(40, 10, content)

	var buf bytes.Buffer
	err := pdf.Output(&buf)
	return buf.Bytes(), err
}

func TestContentAggregationService_AggregateDocuments(t *testing.T) {
	mockPDFProcessor := new(MockPDFProcessor)
	service := NewContentAggregationService("http://mock-arxiv.org/")
	service.pdfProcessor = mockPDFProcessor

	// Setup a mock HTTP server to simulate arXiv
	mockArxiv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pdfContent, err := createTestPDF("Mock arXiv paper content")
		require.NoError(t, err)
		w.WriteHeader(http.StatusOK)
		w.Write(pdfContent)
	}))
	defer mockArxiv.Close()
	service.arxivBaseURL = mockArxiv.URL + "/"

	// Set up expectations
	mockPDFProcessor.On("extractTextFromPDF", mock.Anything).Return("Extracted arXiv content", nil).Once()
	mockPDFProcessor.On("extractTextFromPDF", mock.Anything).Return("Extracted user PDF content", nil).Once()

	arxivIDs := []string{"1234.5678"}
	userPDFs := []string{"user_pdf.pdf"}

	aggregatedContent, err := service.AggregateDocuments(arxivIDs, userPDFs)
	assert.NoError(t, err)
	assert.Contains(t, aggregatedContent, "arXiv:1234.5678")
	assert.Contains(t, aggregatedContent, "Extracted arXiv content")
	assert.Contains(t, aggregatedContent, "User PDF 1")
	assert.Contains(t, aggregatedContent, "Extracted user PDF content")

	mockPDFProcessor.AssertExpectations(t)
}

func TestContentAggregationService_ProcessArXivPaper(t *testing.T) {
	mockPDFProcessor := new(MockPDFProcessor)
	service := NewContentAggregationService("http://mock-arxiv.org/")
	service.pdfProcessor = mockPDFProcessor

	mockArxiv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pdfContent, err := createTestPDF("Mock arXiv paper content")
		assert.NoError(t, err)
		w.WriteHeader(http.StatusOK)
		w.Write(pdfContent)
	}))
	defer mockArxiv.Close()
	service.arxivBaseURL = mockArxiv.URL + "/"

	mockPDFProcessor.On("extractTextFromPDF", mock.Anything).Return("Extracted arXiv content", nil)

	content, err := service.processArXivPaper("1234.5678")

	assert.NoError(t, err)
	assert.Equal(t, "Extracted arXiv content", content)

	mockPDFProcessor.AssertExpectations(t)
}

func TestContentAggregationService_ProcessUserPDF(t *testing.T) {
	mockPDFProcessor := new(MockPDFProcessor)
	service := NewContentAggregationService("")
	service.pdfProcessor = mockPDFProcessor

	mockPDFProcessor.On("extractTextFromPDF", mock.Anything).Return("Extracted user PDF content", nil)

	content, err := service.processUserPDF("user_pdf.pdf")

	assert.NoError(t, err)
	assert.Equal(t, "Extracted user PDF content", content)

	mockPDFProcessor.AssertExpectations(t)
}
