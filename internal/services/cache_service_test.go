package services

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"cloud.google.com/go/storage"
	"cloud.google.com/go/vertexai/genai"
	"github.com/fsouza/fake-gcs-server/fakestorage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// Mock GenAI Client
type MockGenAIClient struct {
	mock.Mock
}

func (m *MockGenAIClient) CreateCachedContent(ctx context.Context, cc *genai.CachedContent) (*genai.CachedContent, error) {
	args := m.Called(ctx, cc)
	return args.Get(0).(*genai.CachedContent), args.Error(1)
}

func (m *MockGenAIClient) Close() error {
	args := m.Called()
	return args.Error(0)
}

// MockStorageClient mocks the StorageClientInterface
type MockStorageClient struct {
	mock.Mock
}

func (m *MockStorageClient) Bucket(name string) *storage.BucketHandle {
	args := m.Called(name)
	return args.Get(0).(*storage.BucketHandle)
}

func (m *MockStorageClient) Close() error {
	args := m.Called()
	return args.Error(0)
}

// Mock BucketHandle
type MockBucketHandle struct {
	mock.Mock
}

func (m *MockBucketHandle) Object(name string) *storage.ObjectHandle {
	args := m.Called(name)
	return args.Get(0).(*storage.ObjectHandle)
}

// Mock ObjectHandle
type MockObjectHandle struct {
	mock.Mock
}

func (m *MockObjectHandle) NewWriter(ctx context.Context) *storage.Writer {
	args := m.Called(ctx)
	return args.Get(0).(*storage.Writer)
}

func TestProcessArXivPaper(t *testing.T) {
	// Create a mock HTTP server to simulate arXiv
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Serve a sample PDF content
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte("%PDF-1.5\n%Test PDF content"))
	}))
	defer server.Close()

	// Create a CacheService with the mock server URL
	cacheService := &CacheService{
		arxivBaseURL: server.URL + "/",
	}

	ctx := context.Background()
	content, err := cacheService.processArXivPaper(ctx, "2104.08730")

	assert.NoError(t, err)
	assert.Contains(t, content, "Test PDF content")

	// Restore the original arXiv URL
	// cacheService.arXivURL = originalArXivURL
}

func TestProcessPDF(t *testing.T) {
	// Create a temporary PDF file for testing
	tempDir, err := os.MkdirTemp("", "pdf-test")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	pdfPath := filepath.Join(tempDir, "test.pdf")
	err = os.WriteFile(pdfPath, []byte("%PDF-1.5\n%Test PDF content"), 0644)
	assert.NoError(t, err)

	cacheService := &CacheService{}
	content, err := cacheService.processPDF(pdfPath)

	assert.NoError(t, err)
	assert.Contains(t, content, "Test PDF content")
}

func TestExtractTextFromPDF(t *testing.T) {
	// Create a temporary PDF file for testing
	tempDir, err := os.MkdirTemp("", "pdf-test")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	pdfPath := filepath.Join(tempDir, "test.pdf")
	err = os.WriteFile(pdfPath, []byte("%PDF-1.5\n%Test PDF content"), 0644)
	assert.NoError(t, err)

	cacheService := &CacheService{}
	content, err := cacheService.extractTextFromPDF(pdfPath)

	assert.NoError(t, err)
	assert.Contains(t, content, "Test PDF content")
}

func TestCreateContentCache(t *testing.T) {
	ctx := context.Background()
	mockGenAIClient := new(MockGenAIClient)
	mockStorageClient := new(MockStorageClient)
	mockBucketHandle := new(MockBucketHandle)
	mockObjectHandle := new(MockObjectHandle)
	mockWriter := &storage.Writer{}

	cacheService := NewCacheService(mockGenAIClient, mockStorageClient)

	// Set up mock expectations
	mockStorageClient.On("Bucket", mock.AnythingOfType("string")).Return(mockBucketHandle)
	mockBucketHandle.On("Object", mock.AnythingOfType("string")).Return(mockObjectHandle)
	mockObjectHandle.On("NewWriter", ctx).Return(mockWriter)
	mockGenAIClient.On("CreateCachedContent", ctx, mock.AnythingOfType("*genai.CachedContent")).Return(&genai.CachedContent{Name: "test-cached-content"}, nil)

	// Create a temporary PDF file for testing
	tempDir, err := os.MkdirTemp("", "pdf-test")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	pdfPath := filepath.Join(tempDir, "test.pdf")
	err = os.WriteFile(pdfPath, []byte("%PDF-1.5\n%Test PDF content"), 0644)
	assert.NoError(t, err)

	// Test inputs
	arxivIDs := []string{"2104.08730"}
	userPDFs := []string{pdfPath}

	// Call the method
	cachedContentName, err := cacheService.CreateContentCache(ctx, arxivIDs, userPDFs)

	// Assertions
	assert.NoError(t, err)
	assert.Equal(t, "test-cached-content", cachedContentName)

	// Verify mock expectations
	mockStorageClient.AssertExpectations(t)
	mockBucketHandle.AssertExpectations(t)
	mockObjectHandle.AssertExpectations(t)
	mockGenAIClient.AssertExpectations(t)
}

func TestUploadToGCS(t *testing.T) {
	// Create a new fake GCS server
	server := fakestorage.NewServer([]fakestorage.Object{})
	defer server.Stop()

	// Create a test bucket
	bucketName := "test-bucket"
	server.CreateBucketWithOpts(fakestorage.CreateBucketOpts{
		Name: bucketName,
	})

	// Create a CacheService with the fake server's client
	cacheService := &CacheService{
		storageClient: server.Client(),
	}

	ctx := context.Background()

	// Test data
	testContents := []string{"Test content 1", "Test content 2"}

	// Call the uploadToGCS method
	gcsURIs, err := cacheService.uploadToGCS(ctx, testContents)
	assert.NoError(t, err)
	assert.Len(t, gcsURIs, 2)

	// Verify that the files were uploaded
	for i, uri := range gcsURIs {
		// Extract the object name from the URI
		objName := uri[len("gs://"+bucketName+"/"):]

		// Get the object
		obj := server.Client().Bucket(bucketName).Object(objName)
		reader, err := obj.NewReader(ctx)
		assert.NoError(t, err)

		// Read the content
		content, err := io.ReadAll(reader)
		assert.NoError(t, err)
		reader.Close()

		// Verify the content
		assert.Equal(t, testContents[i], string(content))
	}
}
