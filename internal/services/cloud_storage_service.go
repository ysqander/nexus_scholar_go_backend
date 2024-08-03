package services

import (
	"context"
	"io"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

type GCSService struct {
	client *storage.Client
}

func NewGCSService(ctx context.Context) (*GCSService, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	return &GCSService{client: client}, nil
}

func (s *GCSService) UploadFile(ctx context.Context, bucketName, objectName string, content io.Reader) error {
	bucket := s.client.Bucket(bucketName)
	obj := bucket.Object(objectName)
	writer := obj.NewWriter(ctx)
	if _, err := io.Copy(writer, content); err != nil {
		return err
	}
	return writer.Close()
}

func (s *GCSService) DownloadFile(ctx context.Context, bucketName, objectName string) ([]byte, error) {
	bucket := s.client.Bucket(bucketName)
	obj := bucket.Object(objectName)
	reader, err := obj.NewReader(ctx)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func (s *GCSService) DeleteFile(ctx context.Context, bucketName, objectName string) error {
	bucket := s.client.Bucket(bucketName)
	obj := bucket.Object(objectName)
	return obj.Delete(ctx)
}

func (s *GCSService) ListFiles(ctx context.Context, bucketName string) ([]string, error) {
	var fileNames []string
	bucket := s.client.Bucket(bucketName)
	it := bucket.Objects(ctx, nil)
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		fileNames = append(fileNames, attrs.Name)
	}
	return fileNames, nil
}
