package security

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var (
	sensitiveName     = regexp.MustCompile(`(?i)(password|passwd|secret|token|authorization|api[-_]?key|private[-_]?key|cookie)`)
	defaultSecretPair = regexp.MustCompile(`(?i)(password|passwd|secret|token|api[-_]?key|private[-_]?key|cookie)(\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;]+)`)
	defaultAuthPair   = regexp.MustCompile(`(?i)(authorization\s*[:=]\s*)((?:bearer|basic|token)\s+)?[^\s,;]+`)
	defaultBearer     = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]+`)
)

type Redactor struct {
	fields   []*regexp.Regexp
	patterns []*regexp.Regexp
}

func NewRedactor(fields, patterns []string) (*Redactor, error) {
	r := &Redactor{}
	for _, field := range fields {
		compiled, err := regexp.Compile("(?i:^" + regexp.QuoteMeta(strings.TrimSpace(field)) + "$)")
		if err != nil {
			return nil, fmt.Errorf("compile redaction field %q: %w", field, err)
		}
		r.fields = append(r.fields, compiled)
	}
	for _, pattern := range patterns {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("compile redaction pattern %q: %w", pattern, err)
		}
		r.patterns = append(r.patterns, compiled)
	}
	return r, nil
}

func DefaultRedactor() *Redactor { redactor, _ := NewRedactor(nil, nil); return redactor }

func IsSensitive(name string) bool { return sensitiveName.MatchString(name) }

func (r *Redactor) IsSensitive(name string) bool {
	if IsSensitive(name) {
		return true
	}
	for _, field := range r.fields {
		if field.MatchString(name) {
			return true
		}
	}
	return false
}

func Mask(value string) string {
	if value == "" {
		return ""
	}
	return "************"
}

func (r *Redactor) RedactHeaders(headers map[string]string) map[string]string {
	result := make(map[string]string, len(headers))
	for key, value := range headers {
		if r.IsSensitive(key) {
			result[key] = Mask(value)
		} else {
			result[key] = r.RedactText(value)
		}
	}
	return result
}

func RedactHeaders(headers map[string]string) map[string]string {
	return DefaultRedactor().RedactHeaders(headers)
}

func (r *Redactor) RedactJSON(data []byte) []byte {
	var value any
	if json.Unmarshal(data, &value) != nil {
		return []byte(r.RedactText(string(data)))
	}
	value = r.RedactValue(value)
	result, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return []byte(r.RedactText(string(data)))
	}
	return result
}

func RedactJSON(data []byte) []byte { return DefaultRedactor().RedactJSON(data) }

func (r *Redactor) RedactText(value string) string {
	value = defaultAuthPair.ReplaceAllString(value, `${1}************`)
	value = defaultSecretPair.ReplaceAllString(value, `${1}${2}************`)
	value = defaultBearer.ReplaceAllString(value, "Bearer ************")
	for _, pattern := range r.patterns {
		value = pattern.ReplaceAllString(value, "************")
	}
	return value
}

func (r *Redactor) RedactValue(value any) any {
	switch current := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(current))
		for key, child := range current {
			if r.IsSensitive(key) {
				result[key] = Mask(fmt.Sprint(child))
			} else {
				result[key] = r.RedactValue(child)
			}
		}
		return result
	case []any:
		result := make([]any, len(current))
		for i, child := range current {
			result[i] = r.RedactValue(child)
		}
		return result
	case string:
		return r.RedactText(current)
	default:
		return current
	}
}

func NormalizeSecretName(name string) string { return strings.TrimSpace(name) }
