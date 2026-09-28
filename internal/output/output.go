package output

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/ward/ward/internal/security"
)

type Envelope struct {
	SchemaVersion int         `json:"schema_version"`
	OK            bool        `json:"ok"`
	Command       string      `json:"command"`
	Project       string      `json:"project,omitempty"`
	Environment   string      `json:"environment,omitempty"`
	Status        string      `json:"status,omitempty"`
	Data          interface{} `json:"data,omitempty"`
	Error         *ErrorInfo  `json:"error,omitempty"`
}

const SchemaVersion = 1

type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
}

type Error struct {
	Code    string
	Message string
	Detail  string
	Exit    int
}

func (e *Error) Error() string { return e.Message }

func UsageError(message string) error { return &Error{Code: "usage_error", Message: message, Exit: 2} }
func ConfigError(message string, detail ...string) error {
	return makeError("config_error", message, detail, 2)
}
func OperationalError(message string, detail ...string) error {
	return makeError("operational_error", message, detail, 1)
}

func makeError(code, message string, detail []string, exit int) error {
	value := ""
	if len(detail) > 0 {
		value = detail[0]
	}
	return &Error{Code: code, Message: message, Detail: value, Exit: exit}
}

func ErrorInfoFrom(err error) *ErrorInfo {
	redactor := security.DefaultRedactor()
	var value *Error
	if errors.As(err, &value) {
		return &ErrorInfo{Code: value.Code, Message: redactor.RedactText(value.Message), Detail: redactor.RedactText(value.Detail)}
	}
	return &ErrorInfo{Code: "error", Message: redactor.RedactText(err.Error())}
}

func WriteJSON(w io.Writer, value any) error {
	if envelope, ok := value.(Envelope); ok {
		if envelope.SchemaVersion == 0 {
			envelope.SchemaVersion = SchemaVersion
		}
		value = envelope
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func WriteTable(w io.Writer, rows [][]string) {
	if len(rows) == 0 {
		return
	}
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for i, cell := range row {
			if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}
	for rowIndex, row := range rows {
		for i, cell := range row {
			if i > 0 {
				fmt.Fprint(w, "  ")
			}
			fmt.Fprintf(w, "%-*s", widths[i], cell)
		}
		fmt.Fprintln(w)
		if rowIndex == 0 {
			for i, width := range widths {
				if i > 0 {
					fmt.Fprint(w, "  ")
				}
				for j := 0; j < width; j++ {
					fmt.Fprint(w, "-")
				}
			}
			fmt.Fprintln(w)
		}
	}
}
