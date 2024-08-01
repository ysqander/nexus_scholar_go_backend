package services_test

import (
	"bytes"
	"nexus_scholar_go_backend/internal/services"
	"os"
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

func createTestPDF(content string) ([]byte, error) {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	pdf.SetFont("Arial", "", 12)
	pdf.Cell(40, 10, content)

	var buf bytes.Buffer
	err := pdf.Output(&buf)
	return buf.Bytes(), err
}

func TestExtractTextFromPDF(t *testing.T) {
	// Create a test PDF with known content
	expectedContent := "This is a test PDF content."
	pdfBytes, err := createTestPDF(expectedContent)
	require.NoError(t, err)

	// Create a temporary file to store the PDF
	tempFile, err := os.CreateTemp("", "test-*.pdf")
	require.NoError(t, err)
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	// Write the PDF bytes to the temporary file
	_, err = tempFile.Write(pdfBytes)
	require.NoError(t, err)

	// Initialize the ContentAggregationService
	service := services.NewContentAggregationService("")

	// Call the ExtractTextFromPDF method
	actualContent, err := service.ExtractTextFromPDF(tempFile.Name())
	require.NoError(t, err)

	// Assert that the extracted content matches the expected content
	assert.Contains(t, actualContent, expectedContent)
}
