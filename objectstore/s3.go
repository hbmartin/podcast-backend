package objectstore

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Object struct {
	Body        []byte
	ContentType string
	ETag        string
}

type Store interface {
	Put(context.Context, string, []byte, string) error
	Get(context.Context, string, int64) (Object, error)
	Delete(context.Context, string) error
}

type S3Store struct {
	client *s3.Client
	bucket string
}

func NewS3Store(ctx context.Context, endpoint, bucket, accessKey, secretKey, region string) (*S3Store, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("load S3 configuration: %w", err)
	}
	client := s3.NewFromConfig(cfg, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(endpoint)
		options.UsePathStyle = true
	})
	return &S3Store{client: client, bucket: bucket}, nil
}

func (s *S3Store) Put(ctx context.Context, key string, body []byte, contentType string) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(key), Body: bytes.NewReader(body),
		ContentType: aws.String(contentType), CacheControl: aws.String("private, no-cache"),
	})
	return err
}

func (s *S3Store) Get(ctx context.Context, key string, limit int64) (Object, error) {
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return Object{}, err
	}
	defer result.Body.Close()
	body, err := io.ReadAll(io.LimitReader(result.Body, limit+1))
	if err != nil {
		return Object{}, err
	}
	if int64(len(body)) > limit {
		return Object{}, fmt.Errorf("object %q exceeds %d bytes", key, limit)
	}
	return Object{Body: body, ContentType: aws.ToString(result.ContentType), ETag: aws.ToString(result.ETag)}, nil
}

func (s *S3Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	return err
}
