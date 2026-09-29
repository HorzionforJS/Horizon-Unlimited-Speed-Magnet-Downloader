package cloud

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3 抽象对象存储接口，便于测试与替换实现。
type S3 interface {
	Upload(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	Presign(ctx context.Context, key string, expiry time.Duration) (string, error)
}

// MinioS3 兼容 S3 协议（AWS S3 / MinIO / 兼容网关）。
type MinioS3 struct {
	client *minio.Client
	bucket string
}

func NewMinioS3(endpoint, accessKey, secretKey, bucket string, useSSL bool) (*MinioS3, error) {
	c, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, err
	}
	return &MinioS3{client: c, bucket: bucket}, nil
}

func (m *MinioS3) Upload(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	_, err := m.client.PutObject(ctx, m.bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	return err
}

func (m *MinioS3) Presign(ctx context.Context, key string, expiry time.Duration) (string, error) {
	u, err := m.client.PresignedGetObject(ctx, m.bucket, key, expiry, nil)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// UploadPath 上传本地文件或目录（目录自动打包为 zip），返回预签名下载 URL。
func UploadPath(ctx context.Context, s3 S3, srcPath, key string, expiry time.Duration) (string, error) {
	info, err := os.Stat(srcPath)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		data, err := zipDir(srcPath)
		if err != nil {
			return "", err
		}
		zk := strings.TrimSuffix(key, "/") + ".zip"
		if err := s3.Upload(ctx, zk, bytes.NewReader(data), int64(len(data)), "application/zip"); err != nil {
			return "", err
		}
		return s3.Presign(ctx, zk, expiry)
	}
	f, err := os.Open(srcPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := s3.Upload(ctx, key, f, info.Size(), "application/octet-stream"); err != nil {
		return "", err
	}
	return s3.Presign(ctx, key, expiry)
}

func zipDir(dir string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		fw, err := zw.Create(rel)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		_, err = fw.Write(data)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
