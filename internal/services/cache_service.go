package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"cloud.google.com/go/storage"
	"cloud.google.com/go/vertexai/genai"
	"github.com/google/uuid"
	"github.com/ledongthuc/pdf"
	"golang.org/x/oauth2/google"
)

type ChatSessionInfo struct {
	Session           *genai.ChatSession
	LastAccessed      time.Time
	CachedContentName string
}

type CacheService struct {
	genaiClient    *genai.Client
	storageClient  *storage.Client
	arxivBaseURL   string
	projectID      string
	location       string
	bucketName     string
	sessions       sync.Map
	expirationTime time.Duration
}

func NewCacheService(ctx context.Context, genaiClient *genai.Client, storageClient *storage.Client, projectID string) (*CacheService, error) {
	const location = "us-central1"
	bucketName := "nexus-scholar-cached-pdfs"

	cs := &CacheService{
		genaiClient:    genaiClient,
		storageClient:  storageClient,
		arxivBaseURL:   "https://arxiv.org/pdf/",
		projectID:      projectID,
		location:       location,
		bucketName:     bucketName,
		expirationTime: 300 * time.Minute,
	}

	// Check if bucket exists, create if it doesn't
	if err := cs.ensureBucketExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to ensure bucket exists: %v", err)
	}

	go cs.periodicCleanup()
	return cs, nil
}

func (s *CacheService) ensureBucketExists(ctx context.Context) error {
	bucket := s.storageClient.Bucket(s.bucketName)
	attrs, err := bucket.Attrs(ctx)
	if err == storage.ErrBucketNotExist {
		log.Printf("Bucket %s does not exist. Creating in %s...", s.bucketName, s.location)
		bucketAttrs := &storage.BucketAttrs{
			Location: s.location,
		}
		if err := bucket.Create(ctx, s.projectID, bucketAttrs); err != nil {
			return fmt.Errorf("failed to create bucket: %v", err)
		}
		log.Printf("Bucket %s created successfully in %s", s.bucketName, s.location)
	} else if err != nil {
		return fmt.Errorf("failed to get bucket attributes: %v", err)
	} else {
		log.Printf("Bucket %s already exists in %s", s.bucketName, attrs.Location)
		if attrs.Location != s.location {
			log.Printf("Warning: Existing bucket is not in %s. It's in %s", s.location, attrs.Location)
		}
	}
	return nil
}

func (s *CacheService) CreateContentCache(ctx context.Context, arxivIDs []string, userPDFs []string, cacheExpirationTTL time.Duration) (string, error) {
	log.Println("Starting CreateContentCache")

	// 1. Process PDFs and extract text content
	log.Println("Processing documents")
	contents, err := s.processDocuments(ctx, arxivIDs, userPDFs)
	if err != nil {
		log.Printf("Error processing documents: %v", err)
		return "", fmt.Errorf("failed to process documents: %v", err)
	}
	log.Println("Documents processed successfully")

	// 2. Upload content to Google Cloud Storage
	log.Println("Uploading content to Google Cloud Storage")
	gcsURIs, err := s.uploadToGCS(ctx, contents)
	if err != nil {
		log.Printf("Error uploading to GCS: %v", err)
		return "", fmt.Errorf("failed to upload to GCS: %v", err)
	}
	log.Println("Content uploaded to Google Cloud Storage successfully")

	// 3. Create cached content
	log.Println("Creating cached content")
	cachedContent, err := s.CreateCachedContentREST(ctx, gcsURIs, cacheExpirationTTL)
	// cachedContent, err := s.createCachedContent(ctx, gcsURIs, cacheExpirationTTL)
	if err != nil {
		log.Printf("Error creating cached content: %v", err)
		return "", fmt.Errorf("failed to create cached content: %v", err)
	}
	log.Println("Cached content created successfully")

	log.Println("CreateContentCache completed successfully")
	return cachedContent.Name, nil
}

func (s *CacheService) processDocuments(ctx context.Context, arxivIDs []string, userPDFs []string) ([]string, error) {
	log.Println("Starting processDocuments")
	var contents []string

	// Process arXiv papers
	for _, id := range arxivIDs {
		log.Printf("Processing arXiv paper: %s", id)
		content, err := s.processArXivPaper(ctx, id)
		if err != nil {
			log.Printf("Error processing arXiv paper %s: %v", id, err)
			return nil, fmt.Errorf("failed to process arXiv paper %s: %v", id, err)
		}
		contents = append(contents, content)
		log.Printf("arXiv paper %s processed successfully", id)
	}

	// Process user-provided PDFs
	for _, pdfPath := range userPDFs {
		log.Printf("Processing user-provided PDF: %s", pdfPath)
		content, err := s.processPDF(pdfPath)
		if err != nil {
			log.Printf("Error processing PDF %s: %v", pdfPath, err)
			return nil, fmt.Errorf("failed to process PDF %s: %v", pdfPath, err)
		}
		contents = append(contents, content)
		log.Printf("PDF %s processed successfully", pdfPath)
	}

	log.Println("processDocuments completed successfully")
	return contents, nil
}

func (s *CacheService) processArXivPaper(ctx context.Context, arxivID string) (string, error) {
	log.Printf("Starting processArXivPaper for arXiv ID: %s", arxivID)

	// Download the PDF from arXiv
	pdfURL := fmt.Sprintf("%s%s.pdf", s.arxivBaseURL, arxivID)
	log.Printf("Downloading PDF from URL: %s", pdfURL)
	resp, err := http.Get(pdfURL)
	if err != nil {
		log.Printf("Error downloading arXiv paper: %v", err)
		return "", fmt.Errorf("failed to download arXiv paper: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Unexpected status code when downloading arXiv paper: %d", resp.StatusCode)
		return "", fmt.Errorf("unexpected status code when downloading arXiv paper: %d", resp.StatusCode)
	}

	// Create a temporary file to store the PDF
	log.Println("Creating temporary file for PDF")
	tempFile, err := os.CreateTemp("", "arxiv-*.pdf")
	if err != nil {
		log.Printf("Error creating temporary file: %v", err)
		return "", fmt.Errorf("failed to create temporary file: %v", err)
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	// Save the PDF content to the temporary file
	log.Println("Saving PDF content to temporary file")
	_, err = io.Copy(tempFile, resp.Body)
	if err != nil {
		log.Printf("Error saving PDF content: %v", err)
		return "", fmt.Errorf("failed to save PDF content: %v", err)
	}

	// Process the PDF file
	log.Println("Extracting text from PDF")
	content, err := s.extractTextFromPDF(tempFile.Name())
	if err != nil {
		log.Printf("Error extracting text from PDF: %v", err)
		return "", fmt.Errorf("failed to extract text from PDF: %v", err)
	}

	log.Printf("processArXivPaper for arXiv ID %s completed successfully", arxivID)
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
			// Instead of returning immediately, we'll continue to the next page
			continue
		}
		content += text
	}

	if content == "" {
		return "", fmt.Errorf("no text content extracted from PDF")
	}

	return content, nil
}

// generateUniqueFilename creates a unique filename using timestamp and UUID
func generateUniqueFilename() string {
	timestamp := time.Now().UnixNano()
	uniqueID := uuid.New().String()
	return fmt.Sprintf("%d-%s.txt", timestamp, uniqueID)
}

func (s *CacheService) uploadToGCS(ctx context.Context, contents []string) ([]string, error) {
	var gcsURIs []string
	for _, content := range contents {
		obj := s.storageClient.Bucket(s.bucketName).Object(generateUniqueFilename())
		writer := obj.NewWriter(ctx)
		// Write content to GCS
		if _, err := writer.Write([]byte(content)); err != nil {
			return nil, fmt.Errorf("failed to write to GCS: %v", err)
		}
		if err := writer.Close(); err != nil {
			return nil, fmt.Errorf("failed to close GCS writer: %v", err)
		}
		gcsURIs = append(gcsURIs, fmt.Sprintf("gs://%s/%s", s.bucketName, obj.ObjectName()))
	}
	return gcsURIs, nil
}

func (s *CacheService) createCachedContent(ctx context.Context, gcsURIs []string, expirationTTL time.Duration) (*genai.CachedContent, error) {
	log.Printf("Creating cached content with %d GCS URIs", len(gcsURIs))

	modelName := fmt.Sprintf("projects/%s/locations/%s/publishers/google/models/gemini-1.5-pro-001", s.projectID, s.location)

	cc := &genai.CachedContent{
		Model: modelName,
		Expiration: genai.ExpireTimeOrTTL{
			TTL: expirationTTL,
		},
	}

	content := &genai.Content{
		Parts: make([]genai.Part, len(gcsURIs)),
	}

	for i, uri := range gcsURIs {
		log.Printf("Adding GCS URI to content: %s", uri)
		content.Parts[i] = genai.FileData{
			MIMEType: "text/plain",
			FileURI:  uri,
		}
	}

	cc.Contents = []*genai.Content{content}

	log.Println("Calling Vertex AI to create cached content")
	cachedContent, err := s.genaiClient.CreateCachedContent(ctx, cc)
	if err != nil {
		log.Printf("Error from Vertex AI while creating cached content: %v", err)
		return nil, fmt.Errorf("Vertex AI error: %v", err)
	}

	log.Printf("Cached content created successfully with name: %s", cachedContent.Name)
	return cachedContent, nil
}

// DeleteCache deletes the cached content with the given cache name
func (s *CacheService) DeleteCache(ctx context.Context, cacheName string) error {
	err := s.genaiClient.DeleteCachedContent(ctx, cacheName)
	if err != nil {
		return fmt.Errorf("failed to delete cached content: %v", err)
	}
	return nil
}

// StartChatSession creates a new chat session using the cached content
func (s *CacheService) StartChatSession(ctx context.Context, cachedContentName string) (string, error) {
	model := s.genaiClient.GenerativeModelFromCachedContent(&genai.CachedContent{Name: cachedContentName})
	session := model.StartChat()
	sessionID := uuid.New().String()

	s.sessions.Store(sessionID, ChatSessionInfo{
		Session:           session,
		LastAccessed:      time.Now(),
		CachedContentName: cachedContentName,
	})

	return sessionID, nil
}

// SendChatMessage sends a message to the chat session and returns the response
func (s *CacheService) SendChatMessage(ctx context.Context, sessionID string, message string) (*genai.GenerateContentResponse, error) {
	sessionInfo, exists := s.getAndUpdateSession(sessionID)
	if !exists {
		return nil, fmt.Errorf("chat session not found")
	}

	return sessionInfo.Session.SendMessage(ctx, genai.Text(message))
}

// StreamChatMessage sends a message to the chat session and streams the response
func (s *CacheService) StreamChatMessage(ctx context.Context, sessionID string, message string) (*genai.GenerateContentResponseIterator, error) {
	sessionInfo, exists := s.getAndUpdateSession(sessionID)
	if !exists {
		return nil, fmt.Errorf("chat session not found")
	}

	return sessionInfo.Session.SendMessageStream(ctx, genai.Text(message)), nil
}

func (s *CacheService) getAndUpdateSession(sessionID string) (ChatSessionInfo, bool) {
	if sessionInterface, ok := s.sessions.Load(sessionID); ok {
		sessionInfo := sessionInterface.(ChatSessionInfo)
		sessionInfo.LastAccessed = time.Now()
		s.sessions.Store(sessionID, sessionInfo)
		return sessionInfo, true
	}
	return ChatSessionInfo{}, false
}

func (s *CacheService) periodicCleanup() {
	ticker := time.NewTicker(15 * time.Minute)
	for range ticker.C {
		s.cleanupExpiredSessions()
	}
}

func (s *CacheService) cleanupExpiredSessions() {
	now := time.Now()
	s.sessions.Range(func(key, value interface{}) bool {
		sessionInfo := value.(ChatSessionInfo)
		if now.Sub(sessionInfo.LastAccessed) > s.expirationTime {
			s.sessions.Delete(key)
		}
		return true
	})
}

// TerminateSession ends the chat session and cleans up resources
func (s *CacheService) TerminateSession(ctx context.Context, sessionID string) error {
	value, exists := s.sessions.LoadAndDelete(sessionID)
	if !exists {
		// Session doesn't exist, but we don't consider this an error
		return nil
	}

	sessionInfo := value.(ChatSessionInfo)

	// Delete the cached content
	err := s.genaiClient.DeleteCachedContent(ctx, sessionInfo.CachedContentName)
	if err != nil {
		// Log the error but don't return it
		log.Printf("Failed to delete cached content for session %s: %v", sessionID, err)
	}

	return nil
}

func (s *CacheService) CreateCachedContentREST(ctx context.Context, gcsURIs []string, cacheExpirationTTL time.Duration) (*genai.CachedContent, error) {
	log.Printf("Creating cached content using REST API with %d GCS URIs", len(gcsURIs))

	accessToken, err := s.getAccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get access token: %v", err)
	}

	requestBody := map[string]interface{}{
		"model": fmt.Sprintf("projects/%s/locations/%s/publishers/google/models/gemini-1.5-pro-001", s.projectID, s.location),
		"contents": []map[string]interface{}{
			{
				"role":  "user",
				"parts": make([]map[string]interface{}, len(gcsURIs)),
			},
		},
		"ttl": map[string]interface{}{
			"seconds": fmt.Sprintf("%d", int64(cacheExpirationTTL.Seconds())),
			"nanos":   "0",
		},
	}

	for i, uri := range gcsURIs {
		requestBody["contents"].([]map[string]interface{})[0]["parts"].([]map[string]interface{})[i] = map[string]interface{}{
			"fileData": map[string]string{
				"mimeType": "text/plain",
				"fileUri":  uri,
			},
		}
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %v", err)
	}

	url := fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1beta1/projects/%s/locations/%s/cachedContents", s.location, s.projectID, s.location)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	// Parse the response into a genai.CachedContent struct
	cachedContent := &genai.CachedContent{
		Name:  result["name"].(string),
		Model: result["model"].(string),
		Expiration: genai.ExpireTimeOrTTL{
			ExpireTime: parseExpireTime(result["expireTime"].(string)),
		},
	}

	// Parse contents if available
	if contentsRaw, ok := result["contents"].([]interface{}); ok {
		cachedContent.Contents = make([]*genai.Content, len(contentsRaw))
		for i, contentRaw := range contentsRaw {
			content := contentRaw.(map[string]interface{})
			cachedContent.Contents[i] = &genai.Content{
				Role: content["role"].(string),
			}
			// Parse parts if available
			if partsRaw, ok := content["parts"].([]interface{}); ok {
				cachedContent.Contents[i].Parts = make([]genai.Part, len(partsRaw))
				for j, partRaw := range partsRaw {
					part := partRaw.(map[string]interface{})
					if fileData, ok := part["fileData"].(map[string]interface{}); ok {
						cachedContent.Contents[i].Parts[j] = genai.FileData{
							MIMEType: fileData["mimeType"].(string),
							FileURI:  fileData["fileUri"].(string),
						}
					} else if text, ok := part["text"].(string); ok {
						cachedContent.Contents[i].Parts[j] = genai.Text(text)
					}
				}
			}
		}
	}

	log.Printf("Cached content created successfully using REST API with name: %s", cachedContent.Name)
	return cachedContent, nil
}

func parseExpireTime(expireTimeStr string) time.Time {
	expireTime, err := time.Parse(time.RFC3339, expireTimeStr)
	if err != nil {
		log.Printf("Failed to parse expireTime: %v", err)
		return time.Time{}
	}
	return expireTime
}

func (s *CacheService) getAccessToken(ctx context.Context) (string, error) {
	tokenSource, err := google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return "", fmt.Errorf("failed to create token source: %v", err)
	}

	token, err := tokenSource.Token()
	if err != nil {
		return "", fmt.Errorf("failed to get token: %v", err)
	}

	return token.AccessToken, nil
}
