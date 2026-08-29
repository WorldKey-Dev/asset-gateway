package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// ErrObjectNotFound：对象键缺失（404）；ErrInvalidRange：Range 不满足（416）。
var (
	ErrObjectNotFound = errors.New("object not found")
	ErrInvalidRange   = errors.New("invalid range")
)

// ObjectData：一次对象读取的结果（透传 Range 语义）。
type ObjectData struct {
	Body          io.ReadCloser
	StatusCode    int // 200 或 206
	ContentLength int64
	ContentRange  string
	ETag          string
}

// ObjectSource：对象存储读取抽象（生产为 S3/MinIO；测试为内存替身）。
type ObjectSource interface {
	Get(ctx context.Context, key, rangeHeader string) (*ObjectData, error)
}

// S3Source：AWS SDK v2 实现；自定义 endpoint 覆盖 MinIO / COS / OSS。
// 凭据与 console 同契约：CONSOLE_S3_{ENDPOINT,BUCKET,ACCESS_KEY,SECRET_KEY,REGION}。
type S3Source struct {
	client *s3.Client
	bucket string
}

func loadAWSConfig(ctx context.Context, cfg Config) (aws.Config, error) {
	opts := []func(*awsconfig.LoadOptions) error{}
	if cfg.S3Region != "" {
		opts = append(opts, awsconfig.WithRegion(cfg.S3Region))
	}
	if cfg.S3AccessKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.S3AccessKey, cfg.S3SecretKey, ""),
		))
	}
	return awsconfig.LoadDefaultConfig(ctx, opts...)
}

func newS3Client(ctx context.Context, cfg Config) (*s3.Client, error) {
	awsCfg, err := loadAWSConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}
	return s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.S3Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.S3Endpoint)
			o.UsePathStyle = true // MinIO / 自建 S3 兼容端点
		}
	}), nil
}

func NewS3Source(ctx context.Context, cfg Config) (*S3Source, error) {
	client, err := newS3Client(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &S3Source{client: client, bucket: cfg.S3Bucket}, nil
}

func (s *S3Source) Get(ctx context.Context, key, rangeHeader string) (*ObjectData, error) {
	in := &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)}
	if rangeHeader != "" {
		in.Range = aws.String(rangeHeader)
	}
	out, err := s.client.GetObject(ctx, in)
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			switch apiErr.ErrorCode() {
			case "NoSuchKey", "NotFound":
				return nil, ErrObjectNotFound
			case "InvalidRange":
				return nil, ErrInvalidRange
			}
		}
		var notFound *s3types.NotFound
		if errors.As(err, &notFound) {
			return nil, ErrObjectNotFound
		}
		return nil, err
	}
	data := &ObjectData{
		Body:          out.Body,
		StatusCode:    200,
		ContentLength: aws.ToInt64(out.ContentLength),
	}
	if out.ContentRange != nil {
		data.StatusCode = 206
		data.ContentRange = *out.ContentRange
	}
	if out.ETag != nil {
		data.ETag = *out.ETag
	}
	return data, nil
}
