package jinn

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestFoldersTravelAsTarGz uploads a folder and downloads it back as an
// output: both ways are one .tar.gz.
func TestFoldersTravelAsTarGz(t *testing.T) {
	var uploaded []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/files":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "file_1", "url": "http://" + r.Host + "/put", "method": "PUT", "headers": map[string]string{}})
		case "/put":
			uploaded, _ = io.ReadAll(r.Body)
		case "/out":
			_, _ = w.Write(uploaded)
		}
	}))
	defer srv.Close()
	src := t.TempDir()
	_ = os.MkdirAll(filepath.Join(src, "sub"), 0o755)
	_ = os.WriteFile(filepath.Join(src, "sub", "a.txt"), []byte("hello"), 0o644)
	c := New("k")
	c.BaseURL = srv.URL
	if _, err := c.UploadFolder(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(uploaded))
	if err != nil {
		t.Fatalf("the upload is not gzip: %v", err)
	}
	if _, err := tar.NewReader(zr).Next(); err != nil {
		t.Fatalf("the upload is not a .tar.gz: %v", err)
	}
	sum := sha256.Sum256(uploaded)
	dst := t.TempDir()
	run := Run{Output: &Output{URL: srv.URL + "/out", SHA256: hex.EncodeToString(sum[:])}}
	if err := c.DownloadOutput(context.Background(), run, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "sub", "a.txt")); string(b) != "hello" {
		t.Fatalf("sub/a.txt = %q", b)
	}
}
