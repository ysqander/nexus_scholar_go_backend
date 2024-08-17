package services

import (
	"context"
	"fmt"
	"io"
	"os"

	"cloud.google.com/go/storage"
	"github.com/rs/zerolog"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

type GCSService struct {
	Client *storage.Client
	logger zerolog.Logger
}

func NewGCSService(ctx context.Context, logger zerolog.Logger) (*GCSService, error) {
	client, err := initGCSClient(ctx)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to initialize GCS client")
		return nil, fmt.Errorf("failed to initialize GCS client: %w", err)
	}
	logger.Info().Msg("GCS client initialized successfully")
	return &GCSService{Client: client, logger: logger}, nil
}

func initGCSClient(ctx context.Context) (*storage.Client, error) {
	if credJSON := os.Getenv("GOOGLE_CREDENTIALS_JSON"); credJSON != "" {
		return storage.NewClient(ctx, option.WithCredentialsJSON([]byte(credJSON)))
	}
	GOOGLE_APPLICATION_CREDENTIALS := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	return storage.NewClient(ctx, option.WithCredentialsFile(GOOGLE_APPLICATION_CREDENTIALS))
}

func (s *GCSService) UploadFile(ctx context.Context, bucketName, objectName string, content io.Reader) error {
	s.logger.Info().Msgf("Uploading file to bucket: %s, object: %s", bucketName, objectName)
	bucket := s.Client.Bucket(bucketName)
	obj := bucket.Object(objectName)
	writer := obj.NewWriter(ctx)
	if _, err := io.Copy(writer, content); err != nil {
		s.logger.Error().Err(err).Msgf("Failed to copy content to GCS object: %s", objectName)
		return fmt.Errorf("failed to copy content to GCS object: %w", err)
	}
	if err := writer.Close(); err != nil {
		s.logger.Error().Err(err).Msgf("Failed to close writer for GCS object: %s", objectName)
		return fmt.Errorf("failed to close writer for GCS object: %w", err)
	}
	s.logger.Info().Msgf("File uploaded successfully to bucket: %s, object: %s", bucketName, objectName)
	return nil
}

func (s *GCSService) DownloadFile(ctx context.Context, bucketName, objectName string) ([]byte, error) {
	s.logger.Info().Msgf("Downloading file from bucket: %s, object: %s", bucketName, objectName)
	bucket := s.Client.Bucket(bucketName)
	obj := bucket.Object(objectName)
	reader, err := obj.NewReader(ctx)
	if err != nil {
		s.logger.Error().Err(err).Msgf("Failed to create reader for GCS object: %s", objectName)
		return nil, fmt.Errorf("failed to create reader for GCS object: %w", err)
	}
	defer reader.Close()
	content, err := io.ReadAll(reader)
	if err != nil {
		s.logger.Error().Err(err).Msgf("Failed to read content from GCS object: %s", objectName)
		return nil, fmt.Errorf("failed to read content from GCS object: %w", err)
	}
	s.logger.Info().Msgf("File downloaded successfully from bucket: %s, object: %s", bucketName, objectName)
	return content, nil
}

func (s *GCSService) DeleteFile(ctx context.Context, bucketName, objectName string) error {
	s.logger.Info().Msgf("Deleting file from bucket: %s, object: %s", bucketName, objectName)
	bucket := s.Client.Bucket(bucketName)
	obj := bucket.Object(objectName)
	if err := obj.Delete(ctx); err != nil {
		s.logger.Error().Err(err).Msgf("Failed to delete GCS object: %s", objectName)
		return fmt.Errorf("failed to delete GCS object: %w", err)
	}
	s.logger.Info().Msgf("File deleted successfully from bucket: %s, object: %s", bucketName, objectName)
	return nil
}

func (s *GCSService) ListFiles(ctx context.Context, bucketName string) ([]string, error) {
	s.logger.Info().Msgf("Listing files in bucket: %s", bucketName)
	var fileNames []string
	bucket := s.Client.Bucket(bucketName)
	it := bucket.Objects(ctx, nil)
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			s.logger.Error().Err(err).Msgf("Failed to iterate over objects in bucket: %s", bucketName)
			return nil, fmt.Errorf("failed to iterate over objects in bucket: %w", err)
		}
		fileNames = append(fileNames, attrs.Name)
	}
	s.logger.Info().Msgf("Listed %d files in bucket: %s", len(fileNames), bucketName)
	return fileNames, nil
}
