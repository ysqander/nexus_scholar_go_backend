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

// const (
// 	testPDFPath = "./TestPdfFiles"
// 	jsonPath    = "./TestPdfFiles/testPdfTitles.json"
// )

// func TestPDFTitleExtraction(t *testing.T) {
// 	// Initialize the ContentAggregationService
// 	cas := services.NewContentAggregationService("https://arxiv.org/pdf/")

// 	// Load the JSON file with expected titles
// 	jsonData, err := os.ReadFile(jsonPath)
// 	if err != nil {
// 		t.Fatalf("Failed to read JSON file: %v", err)
// 	}

// 	var expectedTitles map[string]string
// 	err = json.Unmarshal(jsonData, &expectedTitles)
// 	if err != nil {
// 		t.Fatalf("Failed to unmarshal JSON data: %v", err)
// 	}

// 	// Set a threshold for title similarity (adjust as needed)
// 	const similarityThreshold = 0.8

// 	// Prepare results file
// 	resultsFile, err := os.Create("./testPdfFiles/title_extraction_results.txt")
// 	if err != nil {
// 		t.Fatalf("Failed to create results file: %v", err)
// 	}
// 	defer resultsFile.Close()

// 	passCount := 0
// 	failCount := 0

// 	// Process each PDF file
// 	for filename, expectedTitle := range expectedTitles {
// 		pdfPath := filepath.Join(testPDFPath, filename+".pdf")

// 		// Check if the file exists
// 		if _, err := os.Stat(pdfPath); os.IsNotExist(err) {
// 			failCount++
// 			writeResult(resultsFile, filename, expectedTitle, "", 0, fmt.Sprintf("Error: PDF file not found at %s", pdfPath))
// 			continue
// 		}

// 		// Process the PDF
// 		_, extractedTitle, err := cas.ProcessUserPDF(pdfPath)
// 		if err != nil {
// 			failCount++
// 			writeResult(resultsFile, filename, expectedTitle, extractedTitle, 0, fmt.Sprintf("Error: %v", err))
// 			continue
// 		}

// 		// Calculate similarity using Levenshtein distance
// 		var similarity float64
// 		var comparisonTitle string

// 		// Compare with expected title
// 		distanceExpected := levenshtein.ComputeDistance(expectedTitle, extractedTitle)
// 		maxLenExpected := max(len(expectedTitle), len(extractedTitle))
// 		similarityExpected := 1 - float64(distanceExpected)/float64(maxLenExpected)

// 		// Compare with filename (with .pdf extension)
// 		filenameWithExt := filename + ".pdf"
// 		distanceFilename := levenshtein.ComputeDistance(filenameWithExt, extractedTitle)
// 		maxLenFilename := max(len(filenameWithExt), len(extractedTitle))
// 		similarityFilename := 1 - float64(distanceFilename)/float64(maxLenFilename)

// 		// Use the higher similarity
// 		if similarityExpected > similarityFilename {
// 			similarity = similarityExpected
// 			comparisonTitle = expectedTitle
// 		} else {
// 			similarity = similarityFilename
// 			comparisonTitle = filenameWithExt
// 		}

// 		if similarity < similarityThreshold {
// 			failCount++
// 			writeResult(resultsFile, filename, comparisonTitle, extractedTitle, similarity, "FAIL")
// 		} else {
// 			passCount++
// 			writeResult(resultsFile, filename, comparisonTitle, extractedTitle, similarity, "PASS")
// 		}
// 	}

// 	// Write summary
// 	summary := fmt.Sprintf("\nSummary:\nPassed: %d\nFailed: %d\nTotal: %d", passCount, failCount, passCount+failCount)
// 	resultsFile.WriteString(summary)

// 	// Log summary to test output
// 	t.Logf(summary)

// 	if failCount > 0 {
// 		t.Errorf("Some title extractions failed. Check title_extraction_results.txt for details.")
// 	}
// }

// func writeResult(file *os.File, filename, expected, extracted string, similarity float64, status string) {
// 	result := fmt.Sprintf("File: %s\nExpected: %s\nExtracted: %s\nSimilarity: %.2f\nStatus: %s\n\n",
// 		filename, expected, extracted, similarity, status)
// 	file.WriteString(result)
// }

// func max(a, b int) int {
// 	if a > b {
// 		return a
// 	}
// 	return b
// }
