package main

// MinIO / S3 集成测试（开关：ASSET_GATEWAY_IT_S3_ENDPOINT）。
// 本地：docker compose -f <cloud-console>/infra/compose/base.yml up -d minio
//   ASSET_GATEWAY_IT_S3_ENDPOINT=http://127.0.0.1:9000 go test -run TestIntegrationS3 -v

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestIntegrationS3RoundtripRangeAnd404(t *testing.T) {
	endpoint := os.Getenv("ASSET_GATEWAY_IT_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("set ASSET_GATEWAY_IT_S3_ENDPOINT to run (e.g. http://127.0.0.1:9000)")
	}
	ctx := context.Background()
	cfg := Config{
		S3Endpoint:  endpoint,
		S3Bucket:    "console-it",
		S3AccessKey: "console",
		S3SecretKey: "console123",
		S3Region:    "us-east-1",
	}
	src, err := NewS3Source(ctx, cfg)
	if err != nil {
		t.Fatalf("new source: %v", err)
	}

	// 预置对象（复用 console 集成 bucket）
	client, err := newS3Client(ctx, cfg)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	key := "it/asset-gateway/roundtrip.bin"
	payload := []byte("asset-gateway-integration-payload-0123456789")
	if _, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(cfg.S3Bucket), Key: aws.String(key), Body: bytes.NewReader(payload),
	}); err != nil {
		t.Fatalf("put object: %v", err)
	}

	// 往返一致
	data, err := src.Get(ctx, key, "")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body, _ := io.ReadAll(data.Body)
	data.Body.Close()
	if !bytes.Equal(body, payload) {
		t.Fatalf("roundtrip mismatch: %q", body)
	}
	if data.StatusCode != 200 {
		t.Fatalf("status = %d", data.StatusCode)
	}

	// Range
	data, err = src.Get(ctx, key, "bytes=0-12")
	if err != nil {
		t.Fatalf("range get: %v", err)
	}
	body, _ = io.ReadAll(data.Body)
	data.Body.Close()
	if string(body) != string(payload[:13]) || data.StatusCode != 206 || data.ContentRange == "" {
		t.Fatalf("range = %q status=%d range=%q", body, data.StatusCode, data.ContentRange)
	}

	// 404
	if _, err := src.Get(ctx, "it/asset-gateway/does-not-exist.bin", ""); err != ErrObjectNotFound {
		t.Fatalf("missing object err = %v, want ErrObjectNotFound", err)
	}
}
