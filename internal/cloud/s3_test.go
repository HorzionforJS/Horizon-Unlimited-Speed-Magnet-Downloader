package cloud

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeS3 struct {
	objs map[string][]byte
}

func (f *fakeS3) Upload(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.objs[key] = b
	return nil
}

func (f *fakeS3) Presign(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://s3.example/" + key, nil
}

func TestUploadPathDir(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "b.txt"), []byte("world"), 0o644)

	fs := &fakeS3{objs: map[string][]byte{}}
	url, err := UploadPath(context.Background(), fs, dir, "dl/hash", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://s3.example/dl/hash.zip" {
		t.Fatalf("url = %q, want .../dl/hash.zip", url)
	}
	data, ok := fs.objs["dl/hash.zip"]
	if !ok {
		t.Fatal("未上传 zip 对象")
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if !names["a.txt"] || !names["b.txt"] {
		t.Fatalf("zip 缺少文件: %v", names)
	}
}

func TestUploadPathFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "single.bin")
	_ = os.WriteFile(p, []byte("data"), 0o644)

	fs := &fakeS3{objs: map[string][]byte{}}
	url, err := UploadPath(context.Background(), fs, p, "dl/single.bin", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://s3.example/dl/single.bin" {
		t.Fatalf("url = %q", url)
	}
	if got := string(fs.objs["dl/single.bin"]); got != "data" {
		t.Fatalf("内容 = %q, want data", got)
	}
}
