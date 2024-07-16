package services

import (
	"bytes"
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
	"github.com/jung-kurt/gofpdf"
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

func (m *MockStorageClient) Bucket(name string) BucketHandle {
	args := m.Called(name)
	return args.Get(0).(BucketHandle)
}

func (m *MockStorageClient) Close() error {
	args := m.Called()
	return args.Error(0)
}

// MockBucketHandle mocks the BucketHandle
type MockBucketHandle struct {
	mock.Mock
}

func (m *MockBucketHandle) Object(name string) *storage.ObjectHandle {
	args := m.Called(name)
	return args.Get(0).(*storage.ObjectHandle)
}

// Wrapper for the real storage.Client to implement StorageClientInterface
type StorageClientWrapper struct {
	*storage.Client
}

func (w *StorageClientWrapper) Bucket(name string) BucketHandle {
	return &BucketHandleWrapper{w.Client.Bucket(name)}
}

// Wrapper for the real storage.BucketHandle to implement BucketHandle
type BucketHandleWrapper struct {
	*storage.BucketHandle
}

// Custom Writer for testing
type testWriter struct {
	writeFunc func(p []byte) (n int, err error)
	closeFunc func() error
}

func (w *testWriter) Write(p []byte) (n int, err error) {
	return w.writeFunc(p)
}

func (w *testWriter) Close() error {
	return w.closeFunc()
}

// MockWriter mocks the storage.Writer
type MockWriter struct {
	mock.Mock
	ctx           context.Context
	obj           *storage.ObjectHandle
	bucket        string
	name          string
	attrs         storage.ObjectAttrs
	chunkedUpload bool
}

func (m *MockWriter) Write(p []byte) (n int, err error) {
	args := m.Called(p)
	return args.Int(0), args.Error(1)
}

func (m *MockWriter) Close() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockWriter) ObjectAttrs() *storage.ObjectAttrs {
	return &m.attrs
}

func (m *MockWriter) SetChunkSize(size int) {}

func (m *MockWriter) ChunkSize() int { return 0 }

func createTestPDF(content string) (*bytes.Buffer, error) {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	pdf.SetFont("Arial", "", 12)
	pdf.Cell(40, 10, content)
	var buf bytes.Buffer
	err := pdf.Output(&buf)
	return &buf, err
}

func TestProcessArXivPaper(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pdf := gofpdf.New("P", "mm", "A4", "")
		pdf.AddPage()
		pdf.SetFont("Arial", "", 12)
		pdf.Cell(40, 10, "Test ArXiv PDF content")

		w.Header().Set("Content-Type", "application/pdf")
		err := pdf.Output(w)
		assert.NoError(t, err)
	}))
	defer server.Close()

	cacheService := &CacheService{
		arxivBaseURL: server.URL + "/",
	}

	ctx := context.Background()
	content, err := cacheService.processArXivPaper(ctx, "2104.08730")

	assert.NoError(t, err)
	assert.Contains(t, content, "Test ArXiv PDF content")
}

func TestProcessPDF(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "pdf-test")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	pdfContent := "Test PDF content"
	pdfBuffer, err := createTestPDF(pdfContent)
	assert.NoError(t, err)

	pdfPath := filepath.Join(tempDir, "test.pdf")
	err = os.WriteFile(pdfPath, pdfBuffer.Bytes(), 0644)
	assert.NoError(t, err)

	cacheService := &CacheService{}
	content, err := cacheService.processPDF(pdfPath)

	assert.NoError(t, err)
	assert.Contains(t, content, pdfContent)
}

func TestExtractTextFromPDF(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "pdf-test")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	pdfContent := "Test PDF content"
	pdfBuffer, err := createTestPDF(pdfContent)
	assert.NoError(t, err)

	pdfPath := filepath.Join(tempDir, "test.pdf")
	err = os.WriteFile(pdfPath, pdfBuffer.Bytes(), 0644)
	assert.NoError(t, err)

	cacheService := &CacheService{}
	content, err := cacheService.extractTextFromPDF(pdfPath)

	assert.NoError(t, err)
	assert.Contains(t, content, pdfContent)
}

func TestUploadToGCS(t *testing.T) {
	server := fakestorage.NewServer([]fakestorage.Object{})
	defer server.Stop()

	bucketName := "test-bucket"
	server.CreateBucketWithOpts(fakestorage.CreateBucketOpts{
		Name: bucketName,
	})

	cacheService := &CacheService{
		storageClient: &StorageClientWrapper{server.Client()},
		bucketName:    bucketName,
	}

	ctx := context.Background()
	testContents := []string{"Test content 1", "Test content 2"}

	gcsURIs, err := cacheService.uploadToGCS(ctx, testContents)
	assert.NoError(t, err)
	assert.Len(t, gcsURIs, 2)

	for i, uri := range gcsURIs {
		objName := uri[len("gs://"+bucketName+"/"):]
		obj := server.Client().Bucket(bucketName).Object(objName)
		reader, err := obj.NewReader(ctx)
		assert.NoError(t, err)

		content, err := io.ReadAll(reader)
		assert.NoError(t, err)
		reader.Close()

		assert.Equal(t, testContents[i], string(content))
	}
}
