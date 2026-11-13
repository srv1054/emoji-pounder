package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"mime/multipart"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	version         = "1.1.2"
	defaultEndpoint = "https://slack.com/api/emoji.add"
	maxResponseSize = 1 << 20
)

var invalidNameCharacters = regexp.MustCompile(`[^a-z0-9_-]+`)

type config struct {
	slackToken string
	path       string
	endpoint   string
	logPath    string
	interval   time.Duration
	timeout    time.Duration
	maxRetries int
	recursive  bool
	dryRun     bool
}

type slackResponse struct {
	OK               bool   `json:"ok"`
	Error            string `json:"error"`
	Warning          string `json:"warning"`
	ResponseMetadata struct {
		Messages []string `json:"messages"`
	} `json:"response_metadata"`
}

type uploadResult int

const (
	resultUploaded uploadResult = iota
	resultSkipped
	resultFailed
)

type uploader struct {
	client     *http.Client
	config     config
	logger     *slog.Logger
	nextUpload time.Time
	errorCount int
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	cfg, showVersion, err := parseFlags(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if showVersion {
		fmt.Println("emoji-pounder", version)
		return 0
	}

	logFile, err := os.OpenFile(cfg.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot open log file %q: %v\n", cfg.logPath, err)
		return 1
	}
	defer logFile.Close()

	logger := slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: slog.LevelInfo}))
	files, err := emojiFiles(cfg.path, cfg.recursive)
	if err != nil {
		logger.Error("could not scan emoji path", "path", cfg.path, "error", err)
		fmt.Fprintf(os.Stderr, "could not scan emoji path; see %s\n", cfg.logPath)
		return 1
	}
	if len(files) == 0 {
		logger.Error("no supported emoji files found", "path", cfg.path)
		fmt.Fprintf(os.Stderr, "no PNG, JPG, JPEG, or GIF files found; see %s\n", cfg.logPath)
		return 1
	}

	logger.Info("upload started", "path", cfg.path, "file_count", len(files), "dry_run", cfg.dryRun)
	if cfg.dryRun {
		for _, path := range files {
			logger.Info("would upload emoji", "name", emojiName(path), "path", path)
		}
		logger.Info("validation summary",
			"total", len(files),
			"validated", len(files),
			"processed", 0,
			"successful", 0,
			"skipped", 0,
			"failed", 0,
			"errors", 0,
		)
		fmt.Printf("Summary: total=%d validated=%d processed=0 successful=0 skipped=0 failed=0 errors=0 (dry run). Log: %s\n",
			len(files), len(files), cfg.logPath)
		return 0
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	up := uploader{
		client: &http.Client{Timeout: cfg.timeout},
		config: cfg,
		logger: logger,
	}

	var uploaded, skipped, failed int
	for _, path := range files {
		if ctx.Err() != nil {
			break
		}
		switch up.upload(ctx, path) {
		case resultUploaded:
			uploaded++
		case resultSkipped:
			skipped++
		case resultFailed:
			failed++
		}
	}

	cancelled := ctx.Err() != nil
	processed := uploaded + skipped + failed
	logger.Info("upload summary",
		"total", len(files),
		"processed", processed,
		"successful", uploaded,
		"skipped", skipped,
		"failed", failed,
		"errors", up.errorCount,
		"cancelled", cancelled,
	)
	fmt.Printf("Summary: total=%d processed=%d successful=%d skipped=%d failed=%d errors=%d cancelled=%t. Log: %s\n",
		len(files), processed, uploaded, skipped, failed, up.errorCount, cancelled, cfg.logPath)
	if cancelled || failed > 0 {
		return 1
	}
	return 0
}

func parseFlags(args []string) (config, bool, error) {
	var cfg config
	flags := flag.NewFlagSet("emoji-pounder", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&cfg.slackToken, "slacktoken", "", "Slack user token (required)")
	flags.StringVar(&cfg.path, "path", "", "emoji file or directory (required)")
	flags.StringVar(&cfg.endpoint, "endpoint", defaultEndpoint, "Slack emoji API endpoint")
	flags.StringVar(&cfg.logPath, "log", "emoji-pounder.log", "plain-text log file")
	flags.DurationVar(&cfg.interval, "interval", 3*time.Second, "minimum delay between API calls")
	flags.DurationVar(&cfg.timeout, "timeout", 60*time.Second, "timeout for each API call")
	flags.IntVar(&cfg.maxRetries, "max-retries", 8, "retries for throttling and transient failures")
	flags.BoolVar(&cfg.recursive, "recursive", false, "scan directories recursively")
	flags.BoolVar(&cfg.dryRun, "dry-run", false, "validate files without uploading")
	showVersion := flags.Bool("version", false, "print version")
	flags.BoolVar(showVersion, "v", false, "print version")

	if err := flags.Parse(args); err != nil {
		return cfg, false, fmt.Errorf("%w\n\n%s", err, usage())
	}
	if *showVersion {
		return cfg, true, nil
	}
	if strings.TrimSpace(cfg.slackToken) == "" {
		return cfg, false, fmt.Errorf("-slacktoken is required\n\n%s", usage())
	}
	if strings.TrimSpace(cfg.path) == "" {
		return cfg, false, fmt.Errorf("-path is required\n\n%s", usage())
	}
	if cfg.interval < 0 {
		return cfg, false, errors.New("-interval cannot be negative")
	}
	if cfg.timeout <= 0 {
		return cfg, false, errors.New("-timeout must be greater than zero")
	}
	if cfg.maxRetries < 0 {
		return cfg, false, errors.New("-max-retries cannot be negative")
	}
	return cfg, false, nil
}

func usage() string {
	return `Usage:
  emoji-pounder -slacktoken <token> -path <file-or-directory> [options]

Use -dry-run to validate names and files without contacting Slack.`
}

func emojiFiles(root string, recursive bool) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if !isEmojiFile(root) {
			return nil, fmt.Errorf("%q is not a PNG, JPG, JPEG, or GIF file", root)
		}
		return []string{root}, nil
	}

	var files []string
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && !recursive {
				return filepath.SkipDir
			}
			return nil
		}
		if isEmojiFile(path) {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

func isEmojiFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif":
		return true
	default:
		return false
	}
}

func emojiName(path string) string {
	name := strings.ToLower(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	name = invalidNameCharacters.ReplaceAllString(name, "_")
	return strings.Trim(name, "_-")
}

func (u *uploader) upload(ctx context.Context, path string) uploadResult {
	name := emojiName(path)
	if name == "" {
		u.errorCount++
		u.logger.Error("emoji name is empty after normalization", "path", path)
		return resultFailed
	}

	for attempt := 0; attempt <= u.config.maxRetries; attempt++ {
		if err := u.waitForRateLimit(ctx); err != nil {
			u.errorCount++
			u.logger.Warn("upload cancelled", "name", name, "path", path, "error", err)
			return resultFailed
		}

		response, retryAfter, retryable, err := u.uploadOnce(ctx, name, path)
		if err == nil && response.OK {
			attributes := []any{"name", name, "path", path, "attempt", attempt + 1}
			if response.Warning != "" {
				attributes = append(attributes, "slack_warning", response.Warning)
			}
			u.logger.Info("emoji uploaded", attributes...)
			return resultUploaded
		}
		if err == nil && response.Error == "already_exists" {
			u.logger.Info("emoji skipped",
				"name", name,
				"path", path,
				"reason", "already_exists",
				"attempt", attempt+1,
			)
			return resultSkipped
		}
		u.errorCount++
		if !retryable || attempt == u.config.maxRetries {
			attributes := []any{"name", name, "path", path, "attempts", attempt + 1}
			if err != nil {
				attributes = append(attributes, "error", err)
			} else {
				attributes = append(attributes, "slack_error", response.Error, "warning", response.Warning)
			}
			u.logger.Error("emoji upload failed", attributes...)
			return resultFailed
		}

		delay := retryAfter
		if delay <= 0 {
			delay = retryDelay(attempt)
		}
		attributes := []any{
			"name", name,
			"attempt", attempt + 1,
			"delay", delay,
		}
		if err != nil {
			attributes = append(attributes, "error", err)
		} else {
			attributes = append(attributes, "slack_error", response.Error)
		}
		u.logger.Warn("retrying emoji upload", attributes...)
		if err := sleepContext(ctx, delay); err != nil {
			u.errorCount++
			u.logger.Warn("retry cancelled", "name", name, "path", path, "error", err)
			return resultFailed
		}
	}
	return resultFailed
}

func (u *uploader) waitForRateLimit(ctx context.Context) error {
	delay := time.Until(u.nextUpload)
	if delay > 0 {
		if err := sleepContext(ctx, delay); err != nil {
			return err
		}
	}
	u.nextUpload = time.Now().Add(u.config.interval)
	return nil
}

func (u *uploader) uploadOnce(ctx context.Context, name, path string) (slackResponse, time.Duration, bool, error) {
	var apiResponse slackResponse
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("name", name); err != nil {
		return apiResponse, 0, false, err
	}
	if err := writer.WriteField("mode", "data"); err != nil {
		return apiResponse, 0, false, err
	}

	file, err := os.Open(path)
	if err != nil {
		return apiResponse, 0, false, err
	}
	part, err := writer.CreateFormFile("image", filepath.Base(path))
	if err == nil {
		_, err = io.Copy(part, file)
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = writer.Close()
	}
	if err != nil {
		return apiResponse, 0, false, err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, u.config.endpoint, body)
	if err != nil {
		return apiResponse, 0, false, err
	}
	request.Header.Set("Authorization", "Bearer "+u.config.slackToken)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("User-Agent", "emoji-pounder/"+version)

	httpResponse, err := u.client.Do(request)
	if err != nil {
		return apiResponse, 0, ctx.Err() == nil, err
	}
	defer httpResponse.Body.Close()

	retryAfter := parseRetryAfter(httpResponse.Header.Get("Retry-After"), time.Now())
	decoder := json.NewDecoder(io.LimitReader(httpResponse.Body, maxResponseSize))
	decodeErr := decoder.Decode(&apiResponse)

	if httpResponse.StatusCode == http.StatusTooManyRequests {
		return apiResponse, retryAfter, true, decodeErr
	}
	if httpResponse.StatusCode >= 500 {
		return apiResponse, retryAfter, true, decodeErr
	}
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		if decodeErr != nil {
			return apiResponse, 0, false, fmt.Errorf("Slack returned HTTP %d: %w", httpResponse.StatusCode, decodeErr)
		}
		return apiResponse, 0, false, fmt.Errorf("Slack returned HTTP %d", httpResponse.StatusCode)
	}
	if decodeErr != nil {
		return apiResponse, 0, false, fmt.Errorf("invalid Slack response: %w", decodeErr)
	}

	switch apiResponse.Error {
	case "ratelimited", "internal_error", "service_unavailable", "request_timeout":
		return apiResponse, retryAfter, true, nil
	default:
		return apiResponse, 0, false, nil
	}
}

func retryDelay(attempt int) time.Duration {
	exponent := attempt
	if exponent > 6 {
		exponent = 6
	}
	base := time.Second * time.Duration(1<<exponent)
	jitter := time.Duration(rand.Int63n(int64(base/2) + 1))
	return base + jitter
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(value); err == nil && retryAt.After(now) {
		return retryAt.Sub(now)
	}
	return 0
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
