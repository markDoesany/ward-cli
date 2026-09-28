package logs

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ward/ward/internal/api"
	"github.com/ward/ward/internal/config"
	"github.com/ward/ward/internal/security"
)

type Entry struct {
	Source    string         `json:"source"`
	Timestamp *time.Time     `json:"timestamp,omitempty"`
	Level     string         `json:"level,omitempty"`
	Message   string         `json:"message"`
	User      string         `json:"user,omitempty"`
	Reference string         `json:"reference,omitempty"`
	Fields    map[string]any `json:"fields,omitempty"`
}

type Filter struct {
	Since     time.Time
	Until     time.Time
	Level     string
	User      string
	Reference string
	Search    string
	Limit     int
}

type SearchResult struct {
	Entries []Entry           `json:"entries"`
	Errors  map[string]string `json:"source_errors,omitempty"`
	Count   int               `json:"count"`
}

type followFileState struct {
	offset    int64
	remainder string
	prefix    string
}

func Search(ctx context.Context, env config.EnvironmentConfig, baseDir string, sourceIDs []string, filter Filter, timeout time.Duration) (SearchResult, error) {
	if filter.Limit <= 0 {
		filter.Limit = 500
	}
	wanted := map[string]bool{}
	for _, id := range sourceIDs {
		wanted[id] = true
	}
	redactor, err := security.NewRedactor(env.Redaction.Fields, env.Redaction.Patterns)
	if err != nil {
		return SearchResult{}, err
	}
	result := SearchResult{Entries: []Entry{}, Errors: map[string]string{}}
	foundSource := len(wanted) == 0
	for _, source := range env.Logs.Sources {
		if len(wanted) > 0 && !wanted[source.ID] {
			continue
		}
		foundSource = true
		var entries []Entry
		switch source.Type {
		case "file":
			entries, err = searchFiles(ctx, source, baseDir, filter, redactor)
		case "http":
			entries, err = searchHTTP(ctx, env, source, filter, timeout, redactor)
		default:
			err = fmt.Errorf("unsupported log source type %q", source.Type)
		}
		if err != nil {
			result.Errors[source.ID] = redactor.RedactText(err.Error())
			continue
		}
		result.Entries = append(result.Entries, entries...)
	}
	if !foundSource {
		return SearchResult{}, fmt.Errorf("configured log source not found")
	}
	sort.SliceStable(result.Entries, func(i, j int) bool {
		if result.Entries[i].Timestamp == nil && result.Entries[j].Timestamp == nil {
			return false
		}
		if result.Entries[i].Timestamp == nil {
			return true
		}
		if result.Entries[j].Timestamp == nil {
			return false
		}
		return result.Entries[i].Timestamp.Before(*result.Entries[j].Timestamp)
	})
	if len(result.Entries) > filter.Limit {
		result.Entries = result.Entries[len(result.Entries)-filter.Limit:]
	}
	result.Count = len(result.Entries)
	if result.Count == 0 && len(result.Errors) > 0 {
		return result, fmt.Errorf("all selected log sources failed")
	}
	return result, nil
}

// Follow watches configured file sources and emits newly completed log lines.
// It intentionally does not follow HTTP sources; those require a server-side
// streaming contract and are kept snapshot-only for now.
func Follow(ctx context.Context, env config.EnvironmentConfig, baseDir string, sourceIDs []string, filter Filter, callback func(Entry) error) error {
	if callback == nil {
		return fmt.Errorf("log follow callback is required")
	}
	wanted := map[string]bool{}
	for _, id := range sourceIDs {
		wanted[id] = true
	}
	redactor, err := security.NewRedactor(env.Redaction.Fields, env.Redaction.Patterns)
	if err != nil {
		return err
	}
	selected := []config.LogSourceConfig{}
	found := len(wanted) == 0
	for _, source := range env.Logs.Sources {
		if len(wanted) > 0 && !wanted[source.ID] {
			continue
		}
		found = true
		if source.Type != "file" {
			return fmt.Errorf("log source %q does not support follow mode", source.ID)
		}
		selected = append(selected, source)
	}
	if !found {
		return fmt.Errorf("configured log source not found")
	}
	if len(selected) == 0 {
		return fmt.Errorf("no file log sources are configured")
	}
	states := map[string]*followFileState{}
	for _, source := range selected {
		paths, err := sourcePaths(source, baseDir)
		if err != nil {
			return err
		}
		for _, path := range paths {
			info, statErr := os.Stat(path)
			if statErr != nil {
				if errors.Is(statErr, os.ErrNotExist) {
					continue
				}
				return fmt.Errorf("stat configured log file %s: %w", path, statErr)
			}
			prefix, prefixErr := filePrefix(path)
			if prefixErr != nil {
				return prefixErr
			}
			states[path] = &followFileState{offset: info.Size(), prefix: prefix}
		}
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			for _, source := range selected {
				paths, err := sourcePaths(source, baseDir)
				if err != nil {
					return err
				}
				for _, path := range paths {
					if _, ok := states[path]; !ok {
						states[path] = &followFileState{}
					}
					entries, err := readAppended(source, path, states[path], redactor)
					if err != nil {
						return err
					}
					for _, entry := range entries {
						if matches(entry, filter) {
							if err := callback(entry); err != nil {
								return err
							}
						}
					}
				}
			}
		}
	}
}

func sourcePaths(source config.LogSourceConfig, baseDir string) ([]string, error) {
	paths := []string{}
	for _, pattern := range source.Paths {
		if !filepath.IsAbs(pattern) {
			pattern = filepath.Join(baseDir, pattern)
		}
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid configured log glob: %w", err)
		}
		paths = append(paths, matches...)
	}
	sort.Strings(paths)
	return paths, nil
}

func readAppended(source config.LogSourceConfig, path string, state *followFileState, redactor *security.Redactor) ([]Entry, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat configured log file %s: %w", path, err)
	}
	if info.Size() < state.offset {
		state.offset = 0
		state.remainder = ""
	}
	prefix, err := filePrefix(path)
	if err != nil {
		return nil, err
	}
	if state.prefix != "" && prefix != state.prefix {
		state.offset = 0
		state.remainder = ""
	}
	state.prefix = prefix
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open configured log file %s: %w", path, err)
	}
	defer file.Close()
	if _, err := file.Seek(state.offset, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek configured log file %s: %w", path, err)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("read configured log file %s: %w", path, err)
	}
	state.offset += int64(len(data))
	if len(data) == 0 {
		return nil, nil
	}
	text := state.remainder + string(data)
	lines := strings.Split(text, "\n")
	state.remainder = lines[len(lines)-1]
	entries := []Entry{}
	for _, line := range lines[:len(lines)-1] {
		if entry, ok := parseLine(source, strings.TrimSuffix(line, "\r"), redactor); ok {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

func filePrefix(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open configured log file %s: %w", path, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 512))
	if err != nil {
		return "", fmt.Errorf("read configured log file %s: %w", path, err)
	}
	return string(data), nil
}

func searchFiles(ctx context.Context, source config.LogSourceConfig, baseDir string, filter Filter, redactor *security.Redactor) ([]Entry, error) {
	paths := []string{}
	for _, pattern := range source.Paths {
		if !filepath.IsAbs(pattern) {
			pattern = filepath.Join(baseDir, pattern)
		}
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid configured log glob: %w", err)
		}
		paths = append(paths, matches...)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no configured log files matched source %q", source.ID)
	}
	sort.Strings(paths)
	entries := []Entry{}
	for _, path := range paths {
		select {
		case <-ctx.Done():
			return entries, ctx.Err()
		default:
		}
		file, err := os.Open(path)
		if err != nil {
			return entries, fmt.Errorf("open configured log file %s: %w", path, err)
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			entry, ok := parseLine(source, scanner.Text(), redactor)
			if ok && matches(entry, filter) {
				entries = append(entries, entry)
			}
			select {
			case <-ctx.Done():
				file.Close()
				return entries, ctx.Err()
			default:
			}
		}
		scanErr := scanner.Err()
		file.Close()
		if scanErr != nil {
			return entries, fmt.Errorf("read configured log file %s: %w", path, scanErr)
		}
	}
	return entries, nil
}

func searchHTTP(ctx context.Context, env config.EnvironmentConfig, source config.LogSourceConfig, filter Filter, timeout time.Duration, redactor *security.Redactor) ([]Entry, error) {
	if source.HTTP == nil {
		return nil, fmt.Errorf("HTTP source configuration is missing")
	}
	httpEnv := env
	httpEnv.API = config.APIConfig{BaseURL: source.HTTP.BaseURL, Auth: source.HTTP.Auth, Headers: source.HTTP.Headers, CACertPath: source.HTTP.CACertPath}
	client, err := api.NewClient(ctx, httpEnv, timeout)
	if err != nil {
		return nil, err
	}
	client.Redactor = redactor
	query := url.Values{}
	addMappedQuery(query, source.HTTP.Query, "since", formatTime(filter.Since))
	addMappedQuery(query, source.HTTP.Query, "until", formatTime(filter.Until))
	addMappedQuery(query, source.HTTP.Query, "level", filter.Level)
	addMappedQuery(query, source.HTTP.Query, "user", filter.User)
	addMappedQuery(query, source.HTTP.Query, "reference", filter.Reference)
	addMappedQuery(query, source.HTTP.Query, "search", filter.Search)
	addMappedQuery(query, source.HTTP.Query, "limit", fmt.Sprint(filter.Limit))
	maxBytes := env.Limits.MaxResponseBytes
	if maxBytes <= 0 {
		maxBytes = config.DefaultMaxResponse
	}
	response, err := client.Get(ctx, source.HTTP.Endpoint, query, maxBytes)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("log API returned %s", response.Status)
	}
	return parsePayload(source, []byte(response.Body), filter, redactor)
}

func parsePayload(source config.LogSourceConfig, data []byte, filter Filter, redactor *security.Redactor) ([]Entry, error) {
	if source.Format == "text" {
		entries := []Entry{}
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		for scanner.Scan() {
			if entry, ok := parseLine(source, scanner.Text(), redactor); ok && matches(entry, filter) {
				entries = append(entries, entry)
			}
		}
		return entries, scanner.Err()
	}
	var array []map[string]any
	if json.Unmarshal(data, &array) == nil {
		entries := make([]Entry, 0, len(array))
		for _, object := range array {
			entry := mapEntry(source, object, redactor)
			if matches(entry, filter) {
				entries = append(entries, entry)
			}
		}
		return entries, nil
	}
	entries := []Entry{}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		var object map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &object); err != nil {
			continue
		}
		entry := mapEntry(source, object, redactor)
		if matches(entry, filter) {
			entries = append(entries, entry)
		}
	}
	if err := scanner.Err(); err != nil {
		return entries, err
	}
	return entries, nil
}

func parseLine(source config.LogSourceConfig, line string, redactor *security.Redactor) (Entry, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return Entry{}, false
	}
	if source.Format == "jsonl" {
		var object map[string]any
		if json.Unmarshal([]byte(line), &object) != nil {
			return Entry{}, false
		}
		return mapEntry(source, object, redactor), true
	}
	entry := Entry{Source: source.ID, Message: redactor.RedactText(line)}
	parts := strings.Fields(line)
	if len(parts) > 0 {
		if parsed, err := parseTimestamp(parts[0]); err == nil {
			entry.Timestamp = &parsed
		}
	}
	for _, part := range parts {
		upper := strings.ToUpper(strings.Trim(part, "[]:"))
		if upper == "DEBUG" || upper == "INFO" || upper == "WARN" || upper == "WARNING" || upper == "ERROR" || upper == "FATAL" {
			entry.Level = upper
			break
		}
	}
	return entry, true
}

func mapEntry(source config.LogSourceConfig, object map[string]any, redactor *security.Redactor) Entry {
	fields := map[string]any{}
	for key, value := range object {
		fields[key] = redactor.RedactValue(value)
	}
	entry := Entry{Source: source.ID, Fields: fields}
	if parsed, err := parseTimestamp(stringField(object, fieldOr(source.Fields.Timestamp, "timestamp"))); err == nil {
		entry.Timestamp = &parsed
	}
	entry.Level = stringField(object, fieldOr(source.Fields.Level, "level"))
	entry.Message = redactor.RedactText(stringField(object, fieldOr(source.Fields.Message, "message")))
	entry.User = redactor.RedactText(stringField(object, fieldOr(source.Fields.User, "user")))
	entry.Reference = redactor.RedactText(stringField(object, fieldOr(source.Fields.Reference, "reference")))
	return entry
}

func matches(entry Entry, filter Filter) bool {
	if !filter.Since.IsZero() && entry.Timestamp != nil && entry.Timestamp.Before(filter.Since) {
		return false
	}
	if !filter.Until.IsZero() && entry.Timestamp != nil && entry.Timestamp.After(filter.Until) {
		return false
	}
	if filter.Level != "" && !strings.EqualFold(entry.Level, filter.Level) {
		return false
	}
	if filter.User != "" && !strings.EqualFold(entry.User, filter.User) && !strings.Contains(strings.ToLower(entry.Message), strings.ToLower(filter.User)) {
		return false
	}
	if filter.Reference != "" && !strings.EqualFold(entry.Reference, filter.Reference) && !strings.Contains(strings.ToLower(entry.Message), strings.ToLower(filter.Reference)) {
		return false
	}
	if filter.Search != "" && !strings.Contains(strings.ToLower(entry.Message), strings.ToLower(filter.Search)) {
		return false
	}
	return true
}

func ExportJSONL(path string, result SearchResult, force bool) error {
	flags := os.O_CREATE | os.O_WRONLY
	if force {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	file, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("output file already exists; use --force to overwrite")
		}
		return err
	}
	encoder := json.NewEncoder(file)
	for _, entry := range result.Entries {
		if err := encoder.Encode(entry); err != nil {
			file.Close()
			return err
		}
	}
	return file.Close()
}

func addMappedQuery(values url.Values, mapping map[string]string, logical, value string) {
	if value == "" {
		return
	}
	key := mapping[logical]
	if key == "" {
		key = logical
	}
	values.Set(key, value)
}
func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}
func parseTimestamp(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized timestamp")
}
func stringField(object map[string]any, key string) string {
	if value, ok := object[key]; ok {
		return fmt.Sprint(value)
	}
	return ""
}
func fieldOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
