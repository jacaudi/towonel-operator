package controller

// secret is a string that redacts itself in logs, fmt output, and JSON.
// Call Expose only at the boundary where the plaintext is actually needed.
type secret string

func (secret) String() string               { return "[REDACTED]" }
func (secret) GoString() string             { return "[REDACTED]" }
func (secret) MarshalJSON() ([]byte, error) { return []byte(`"[REDACTED]"`), nil }

// Expose returns the underlying plaintext. Use only at API/Secret boundaries.
func (s secret) Expose() string { return string(s) }

// MarshalText guards encoding.TextMarshaler consumers (slog text handlers,
// some structured-log encoders) from leaking the plaintext.
func (secret) MarshalText() ([]byte, error) { return []byte("[REDACTED]"), nil }
