package controller

import (
	"errors"
	"fmt"
	"strings"
)

// ErrUnrecognizedTruthy is returned by ParseTruthy for out-of-vocabulary values.
var ErrUnrecognizedTruthy = errors.New("unrecognized truthy value")

// ParseTruthy interprets an annotation value as a boolean (case-insensitive,
// trimmed). Mirrors cloudflare-operator's vocabulary so annotations read native.
func ParseTruthy(v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "yes", "enable", "enabled":
		return true, nil
	case "false", "no", "disable", "disabled":
		return false, nil
	default:
		return false, fmt.Errorf("%w: %q", ErrUnrecognizedTruthy, v)
	}
}
