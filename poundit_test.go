package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestEmojiName(t *testing.T) {
	tests := map[string]string{
		"Party Parrot.gif":      "party_parrot",
		"thumbs-UP!!.PNG":       "thumbs-up",
		"already_valid-2.jpg":   "already_valid-2",
		"___surrounded___.jpeg": "surrounded",
		"🔥.png":                 "",
	}
	for input, want := range tests {
		if got := emojiName(input); got != want {
			t.Errorf("emojiName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestEmojiFiles(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "one.PNG"))
	writeTestFile(t, filepath.Join(root, "ignore.txt"))
	writeTestFile(t, filepath.Join(root, "nested", "two.gif"))

	files, err := emojiFiles(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || filepath.Base(files[0]) != "one.PNG" {
		t.Fatalf("non-recursive files = %v", files)
	}

	files, err = emojiFiles(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("recursive file count = %d, want 2", len(files))
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, time.November, 13, 12, 0, 0, 0, time.UTC)
	if got := parseRetryAfter("17", now); got != 17*time.Second {
		t.Errorf("seconds Retry-After = %s", got)
	}
	date := now.Add(30 * time.Second).Format(http.TimeFormat)
	if got := parseRetryAfter(date, now); got != 30*time.Second {
		t.Errorf("date Retry-After = %s", got)
	}
	if got := parseRetryAfter("invalid", now); got != 0 {
		t.Errorf("invalid Retry-After = %s", got)
	}
}

func TestUploadRetriesRateLimitAndSendsMultipart(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		call := calls.Add(1)
		if got := request.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization = %q", got)
		}
		if call == 1 {
			response.Header().Set("Retry-After", "0")
			response.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(response, `{"ok":false,"error":"ratelimited"}`)
			return
		}

		reader, err := request.MultipartReader()
		if err != nil {
			t.Fatal(err)
		}
		fields := readMultipart(t, reader)
		if fields["name"] != "party_parrot" {
			t.Errorf("name = %q", fields["name"])
		}
		if fields["mode"] != "data" {
			t.Errorf("mode = %q", fields["mode"])
		}
		if fields["image"] != "image-data" {
			t.Errorf("image = %q", fields["image"])
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(slackResponse{OK: true})
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "Party Parrot.png")
	if err := os.WriteFile(path, []byte("image-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	up := uploader{
		client: server.Client(),
		config: config{
			slackToken: "secret",
			endpoint:   server.URL,
			maxRetries: 1,
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	if got := up.upload(context.Background(), path); got != resultUploaded {
		t.Fatalf("upload result = %v, want uploaded", got)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("calls = %d, want 2", got)
	}
	if up.errorCount != 1 {
		t.Fatalf("error count = %d, want 1", up.errorCount)
	}
}

func TestUploadSkipsExistingEmoji(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(response, `{"ok":false,"error":"already_exists"}`)
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "existing.png")
	writeTestFile(t, path)
	var logOutput bytes.Buffer
	up := uploader{
		client: server.Client(),
		config: config{
			slackToken: "secret",
			endpoint:   server.URL,
		},
		logger: slog.New(slog.NewTextHandler(&logOutput, nil)),
	}

	if got := up.upload(context.Background(), path); got != resultSkipped {
		t.Fatalf("upload result = %v, want skipped", got)
	}
	logged := logOutput.String()
	for _, want := range []string{"time=", "level=INFO", `msg="emoji skipped"`, "reason=already_exists"} {
		if !strings.Contains(logged, want) {
			t.Errorf("skip log %q does not contain %q", logged, want)
		}
	}
	if up.errorCount != 0 {
		t.Fatalf("error count = %d, want 0", up.errorCount)
	}
}

func readMultipart(t *testing.T, reader *multipart.Reader) map[string]string {
	t.Helper()
	fields := make(map[string]string)
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return fields
		}
		if err != nil {
			t.Fatal(err)
		}
		value, err := io.ReadAll(part)
		if err != nil {
			t.Fatal(err)
		}
		fields[part.FormName()] = string(value)
	}
}

func writeTestFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 8)), 0o600); err != nil {
		t.Fatal(err)
	}
}
