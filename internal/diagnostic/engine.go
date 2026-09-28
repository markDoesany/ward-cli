package diagnostic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ward/ward/internal/config"
)

type Executor interface {
	Health(context.Context, string) (any, error)
	APIGet(context.Context, string) (any, error)
	DatabaseQuery(context.Context, string, []string) (any, error)
	LogSearch(context.Context, string, map[string]string) (any, error)
}

type StepResult struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Status   string `json:"status"`
	Data     any    `json:"data,omitempty"`
	Error    string `json:"error,omitempty"`
	Duration int64  `json:"duration_ms"`
}

type Finding struct {
	Severity       string `json:"severity"`
	Issue          string `json:"issue"`
	Evidence       string `json:"evidence,omitempty"`
	Recommendation string `json:"recommendation,omitempty"`
	Step           string `json:"step"`
}

type Result struct {
	Workflow string       `json:"workflow"`
	Status   string       `json:"status"`
	Steps    []StepResult `json:"steps"`
	Findings []Finding    `json:"findings,omitempty"`
}

func Run(ctx context.Context, name string, definition config.DiagnosticDefinition, rawParams map[string]string, executor Executor) (Result, error) {
	params, err := validateParameters(definition.Parameters, rawParams)
	if err != nil {
		return Result{}, err
	}
	result := Result{Workflow: name, Status: "HEALTHY", Steps: []StepResult{}}
	dataByStep := map[string]any{}
	for _, step := range definition.Steps {
		started := time.Now()
		stepResult := StepResult{ID: step.ID, Type: step.Type, Status: "HEALTHY"}
		var data any
		switch step.Type {
		case "health":
			data, err = executor.Health(ctx, render(step.Service, params, false))
		case "api":
			data, err = executor.APIGet(ctx, render(step.Path, params, true))
		case "database":
			args := make([]string, len(step.Arguments))
			for i, value := range step.Arguments {
				args[i] = render(value, params, false)
			}
			data, err = executor.DatabaseQuery(ctx, step.Query, args)
		case "logs":
			filters := map[string]string{}
			for key, value := range step.Filters {
				filters[key] = render(value, params, false)
			}
			data, err = executor.LogSearch(ctx, step.Source, filters)
		default:
			err = fmt.Errorf("unsupported diagnostic step type %q", step.Type)
		}
		stepResult.Duration = time.Since(started).Milliseconds()
		if err != nil {
			stepResult.Status, stepResult.Error = "UNKNOWN", err.Error()
			result.Status = mergeStatus(result.Status, "UNKNOWN")
			result.Steps = append(result.Steps, stepResult)
			if !step.ContinueOnError {
				break
			}
			continue
		}
		stepResult.Data = data
		normalized := normalize(data)
		dataByStep[step.ID] = normalized
		if status := nestedStatus(normalized); status != "" {
			stepResult.Status = status
			result.Status = mergeStatus(result.Status, status)
		}
		result.Steps = append(result.Steps, stepResult)
	}
	for _, assertion := range definition.Assertions {
		value, exists := selectValue(dataByStep[assertion.Step], assertion.Selector)
		matched, compareErr := compare(value, exists, assertion.Operator, assertion.Value)
		if compareErr != nil {
			return result, fmt.Errorf("assertion for step %s: %w", assertion.Step, compareErr)
		}
		if !matched {
			continue
		}
		result.Findings = append(result.Findings, Finding{Severity: assertion.Severity, Issue: render(assertion.Issue, params, false), Evidence: render(assertion.Evidence, params, false), Recommendation: render(assertion.Recommendation, params, false), Step: assertion.Step})
		if assertion.Severity == "critical" {
			result.Status = "UNHEALTHY"
		} else if result.Status == "HEALTHY" {
			result.Status = "DEGRADED"
		}
	}
	return result, nil
}

func validateParameters(definitions []config.QueryParameter, values map[string]string) (map[string]string, error) {
	result := map[string]string{}
	for _, definition := range definitions {
		value, ok := values[definition.Name]
		if !ok {
			return nil, fmt.Errorf("required diagnostic parameter %s is missing", definition.Name)
		}
		switch definition.Type {
		case "int", "int64":
			if _, err := strconv.ParseInt(value, 10, 64); err != nil {
				return nil, fmt.Errorf("parameter %s must be an integer", definition.Name)
			}
		case "bool":
			if _, err := strconv.ParseBool(value); err != nil {
				return nil, fmt.Errorf("parameter %s must be boolean", definition.Name)
			}
		case "time":
			if _, err := time.Parse(time.RFC3339, value); err != nil {
				return nil, fmt.Errorf("parameter %s must be RFC3339 time", definition.Name)
			}
		case "string":
		default:
			return nil, fmt.Errorf("parameter %s has unsupported type %q", definition.Name, definition.Type)
		}
		result[definition.Name] = value
	}
	for key := range values {
		found := false
		for _, definition := range definitions {
			if definition.Name == key {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("unknown diagnostic parameter %s", key)
		}
	}
	return result, nil
}

func render(value string, params map[string]string, escapePath bool) string {
	for key, replacement := range params {
		if escapePath {
			replacement = url.PathEscape(replacement)
		}
		value = strings.ReplaceAll(value, "${"+key+"}", replacement)
	}
	return value
}

func normalize(value any) any {
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var result any
	if json.Unmarshal(data, &result) != nil {
		return value
	}
	return result
}

func nestedStatus(value any) string {
	object, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	status, ok := object["status"].(string)
	if !ok {
		return ""
	}
	status = strings.ToUpper(status)
	if status == "HEALTHY" || status == "DEGRADED" || status == "UNHEALTHY" || status == "UNKNOWN" {
		return status
	}
	return ""
}

func selectValue(value any, selector string) (any, bool) {
	if selector == "" {
		return value, value != nil
	}
	current := value
	for _, part := range strings.Split(selector, ".") {
		switch typed := current.(type) {
		case map[string]any:
			var ok bool
			current, ok = typed[part]
			if !ok {
				return nil, false
			}
		default:
			return nil, false
		}
	}
	return current, true
}

func compare(actual any, exists bool, operator string, expected any) (bool, error) {
	switch operator {
	case "exists":
		return exists, nil
	case "equals":
		return exists && fmt.Sprint(actual) == fmt.Sprint(expected), nil
	case "contains":
		return exists && strings.Contains(strings.ToLower(fmt.Sprint(actual)), strings.ToLower(fmt.Sprint(expected))), nil
	case "gt", "gte", "lt", "lte":
		if !exists {
			return false, nil
		}
		left, err := strconv.ParseFloat(fmt.Sprint(actual), 64)
		if err != nil {
			return false, fmt.Errorf("selected value is not numeric")
		}
		right, err := strconv.ParseFloat(fmt.Sprint(expected), 64)
		if err != nil {
			return false, fmt.Errorf("assertion value is not numeric")
		}
		switch operator {
		case "gt":
			return left > right, nil
		case "gte":
			return left >= right, nil
		case "lt":
			return left < right, nil
		default:
			return left <= right, nil
		}
	default:
		return false, fmt.Errorf("unsupported operator %q", operator)
	}
}

func mergeStatus(current, candidate string) string {
	rank := map[string]int{"HEALTHY": 0, "UNKNOWN": 1, "DEGRADED": 2, "UNHEALTHY": 3}
	if rank[candidate] > rank[current] {
		return candidate
	}
	return current
}
