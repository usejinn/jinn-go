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
	"strings"
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

// Exec sends a bash command and long-polls it to its result; Finish reads
// whether the run's output was accepted.
func TestExecAndFinish(t *testing.T) {
	var sent []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/v1/runs/run_1/commands":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			sent = append(sent, body)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "cmd_1", "run": "run_1", "kind": body["kind"], "state": "queued"})
		case r.URL.Path == "/v1/runs/run_1/commands/cmd_1" && r.URL.Query().Get("wait") == "25":
			result := map[string]any{"exit_code": 3, "output": "boom", "output_bytes": 4}
			if sent[len(sent)-1]["kind"] == KindFinish {
				result = map[string]any{"accepted": false, "problems": []string{"report.md is missing"}}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "cmd_1", "state": "succeeded", "result": result})
		default:
			http.Error(w, `{"code":"not_found","error":"no"}`, 404)
		}
	}))
	defer srv.Close()
	c := New("k")
	c.BaseURL = srv.URL
	res, err := c.Exec(context.Background(), "run_1", BashArgs{Command: "make", Env: map[string]string{"CI": "1"}})
	if err != nil || res.ExitCode != 3 || res.Output != "boom" {
		t.Fatalf("exec %+v %v", res, err)
	}
	if args := sent[0]["arguments"].(map[string]any); sent[0]["kind"] != "bash" || args["command"] != "make" || args["env"].(map[string]any)["CI"] != "1" {
		t.Fatalf("sent %v", sent[0])
	}
	f, err := c.Finish(context.Background(), "run_1", FinishArgs{State: Succeeded})
	if err != nil || f.Accepted || f.Problems[0] != "report.md is missing" {
		t.Fatalf("finish %+v %v", f, err)
	}
	if _, err := c.Run(context.Background(), "run_x"); err == nil || err.(*Error).Code != "not_found" {
		t.Fatalf("error %v", err)
	}
}

// IdempotencyKey travels as the Idempotency-Key header, never in the body.
func TestStartRunSendsTheIdempotencyKey(t *testing.T) {
	var header, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Get("Idempotency-Key")
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"run_1","state":"queued"}`))
	}))
	defer srv.Close()
	c := New("jinn_test")
	c.BaseURL = srv.URL
	if _, err := c.StartRun(context.Background(), "fnc_1", RunRequest{Prompt: "x", IdempotencyKey: "evt_1"}); err != nil {
		t.Fatal(err)
	}
	if header != "evt_1" || strings.Contains(body, "evt_1") || strings.Contains(body, "dempotency") {
		t.Fatalf("header %q, body %s", header, body)
	}
	if _, err := c.StartRun(context.Background(), "fnc_1", RunRequest{Prompt: "x"}); err != nil || header != "" {
		t.Fatalf("no key sends no header: %q, %v", header, err)
	}
}
