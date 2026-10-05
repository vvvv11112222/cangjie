package apperror

import "fmt"

type Error struct {
	Status  int
	Code    string
	Message string
	Details map[string]any
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message, Details: map[string]any{}}
}

func WithDetails(status int, code, message string, details map[string]any) *Error {
	return &Error{Status: status, Code: code, Message: message, Details: details}
}
