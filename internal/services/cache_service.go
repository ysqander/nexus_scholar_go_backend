package services

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	"cloud.google.com/go/storage"
	"cloud.google.com/go/vertexai/genai"
	"github.com/google/uuid"
	"github.com/ledongthuc/pdf"
)

// GenAIClientInterface defines the methods we use from genai.Client
type GenAIClientInterface interface {
	CreateCachedContent(ctx context.Context, cc *genai.CachedContent) (*genai.CachedContent, error)
	Close() error
}

// StorageClientInterface defines the methods we use from storage.Client
type StorageClientInterface interface {
	Bucket(name string) *storage.BucketHandle
	Close() error
}

type CacheService struct {
	genaiClient   GenAIClientInterface
	storageClient StorageClientInterface
	arxivBaseURL  string
}

func NewCacheService(genaiClient GenAIClientInterface, storageClient StorageClientInterface) *CacheService {
	return &CacheService{
		genaiClient:   genaiClient,
		storageClient: storageClient,
		arxivBaseURL:  "https://arxiv.org/pdf/",
	}
}

func (s *CacheService) CreateContentCache(ctx context.Context, arxivIDs []string, userPDFs []string) (string, error) {
	// 1. Process PDFs and extract text content
	contents, err := s.processDocuments(ctx, arxivIDs, userPDFs)
	if err != nil {
		return "", fmt.Errorf("failed to process documents: %v", err)
	}

	// 2. Upload content to Google Cloud Storage
	gcsURIs, err := s.uploadToGCS(ctx, contents)
	if err != nil {
		return "", fmt.Errorf("failed to upload to GCS: %v", err)
	}

	// 3. Create cached content
	cachedContent, err := s.createCachedContent(ctx, gcsURIs)
	if err != nil {
		return "", fmt.Errorf("failed to create cached content: %v", err)
	}

	return cachedContent.Name, nil
}

func (s *CacheService) processDocuments(ctx context.Context, arxivIDs []string, userPDFs []string) ([]string, error) {
	var contents []string

	// Process arXiv papers
	for _, id := range arxivIDs {
		content, err := s.processArXivPaper(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("failed to process arXiv paper %s: %v", id, err)
		}
		contents = append(contents, content)
	}

	// Process user-provided PDFs
	for _, pdfPath := range userPDFs {
		content, err := s.processPDF(pdfPath)
		if err != nil {
			return nil, fmt.Errorf("failed to process PDF %s: %v", pdfPath, err)
		}
		contents = append(contents, content)
	}

	return contents, nil
}

func (s *CacheService) processArXivPaper(ctx context.Context, arxivID string) (string, error) {
	// Download the PDF from arXiv
	pdfURL := fmt.Sprintf("%s%s.pdf", s.arxivBaseURL, arxivID)
	resp, err := http.Get(pdfURL)
	if err != nil {
		return "", fmt.Errorf("failed to download arXiv paper: %v", err)
	}
	defer resp.Body.Close()

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

func (s *CacheService) processPDF(pdfPath string) (string, error) {
	return s.extractTextFromPDF(pdfPath)
}

func (s *CacheService) extractTextFromPDF(pdfPath string) (string, error) {
	f, r, err := pdf.Open(pdfPath)
	if err != nil {
		return "", fmt.Errorf("failed to open PDF: %v", err)
	}
	defer f.Close()

	var content string
	totalPage := r.NumPage()

	for pageIndex := 1; pageIndex <= totalPage; pageIndex++ {
		p := r.Page(pageIndex)
		if p.V.IsNull() {
			continue
		}

		text, err := p.GetPlainText(nil)
		if err != nil {
			return "", fmt.Errorf("failed to get text from page %d: %v", pageIndex, err)
		}
		content += text
	}

	return content, nil
}

func (s *CacheService) uploadToGCS(ctx context.Context, contents []string) ([]string, error) {
	var gcsURIs []string
	bucketName := "nexus-scholar-pdftexts" // Replace with your actual bucket name

	for i, content := range contents {
		filename := fmt.Sprintf("content_%d_%s.txt", i, uuid.New().String())
		obj := s.storageClient.Bucket(bucketName).Object(filename)
		writer := obj.NewWriter(ctx)

		_, err := io.WriteString(writer, content)
		if err != nil {
			return nil, fmt.Errorf("failed to write content to GCS: %v", err)
		}

		if err := writer.Close(); err != nil {
			return nil, fmt.Errorf("failed to close GCS writer: %v", err)
		}

		gcsURIs = append(gcsURIs, fmt.Sprintf("gs://%s/%s", bucketName, filename))
	}

	return gcsURIs, nil
}

func (s *CacheService) createCachedContent(ctx context.Context, gcsURIs []string) (*genai.CachedContent, error) {
	cc := &genai.CachedContent{
		Model: "gemini-1.5-pro-001", // Specify the Gemini model you want to use
	}

	// Create a single Content object with all GCS URIs
	content := &genai.Content{
		Parts: make([]genai.Part, len(gcsURIs)),
	}

	for i, uri := range gcsURIs {
		content.Parts[i] = genai.FileData{
			MIMEType: "text/plain",
			FileURI:  uri,
		}
	}

	cc.Contents = []*genai.Content{content}

	return s.genaiClient.CreateCachedContent(ctx, cc)
}
