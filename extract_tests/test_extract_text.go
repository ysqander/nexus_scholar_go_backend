package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"nexus_scholar_go_backend/internal/services"
)

func main() {
	// Check if poppler utils is installed
	if _, err := exec.LookPath("pdftotext"); err != nil {
		log.Fatalf("Error: poppler utils (pdftotext) is not installed or not in PATH: %v", err)
	}
	fmt.Println("poppler utils (pdftotext) is installed")

	fmt.Println("Starting main function")

	// Create a new ContentAggregationService
	cas := services.NewContentAggregationService("")
	fmt.Println("ContentAggregationService created")

	// List of PDF files to test
	pdfFiles := []string{
		"2206.07682v2.pdf",
		"2307.02046v6.pdf",
		"2307.15818v1.pdf",
	}
	fmt.Printf("PDF files to process: %v\n", pdfFiles)

	// Get the current working directory
	currentDir, err := os.Getwd()
	if err != nil {
		log.Fatalf("Error getting current directory: %v", err)
	}
	fmt.Printf("Current working directory: %s\n", currentDir)

	// Create test_texts directory if it doesn't exist
	testDir := filepath.Join(currentDir, "test_texts")
	fmt.Printf("Creating test directory: %s\n", testDir)
	if err := os.MkdirAll(testDir, 0755); err != nil {
		log.Fatalf("Error creating directory %s: %v", testDir, err)
	}
	fmt.Println("Test directory created successfully")

	// Iterate through each PDF file
	for _, pdfFileName := range pdfFiles {
		pdfPath := filepath.Join(currentDir, "extract_tests", pdfFileName)
		fmt.Printf("Processing %s:\n", pdfFileName)
		fmt.Printf("Full path: %s\n", pdfPath)

		// Extract text from the PDF
		fmt.Printf("Extracting text from %s\n", pdfFileName)
		content, err := cas.ExtractTextFromPDF(pdfPath)
		if err != nil {
			log.Printf("Error extracting text from %s: %v\n", pdfFileName, err)
			continue
		}
		fmt.Printf("Text extracted successfully from %s\n", pdfFileName)

		// Save content to a text file
		outputFile := filepath.Join(testDir, fmt.Sprintf("%s.txt", pdfFileName))
		fmt.Printf("Saving content to %s\n", outputFile)
		if err := os.WriteFile(outputFile, []byte(content), 0644); err != nil {
			log.Printf("Error writing content to file %s: %v\n", outputFile, err)
			continue
		}

		fmt.Printf("Content saved to %s\n\n", outputFile)
	}

	fmt.Println("Main function completed")
}
