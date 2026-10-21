package reqctx

// maxRequestIDLen bounds an inbound request ID. Generated IDs are 36-char
// UUIDs; 128 leaves room for whatever format an upstream gateway uses.
const maxRequestIDLen = 128

// ValidRequestID reports whether s is acceptable as a request ID taken
// from an inbound header or gRPC metadata value: 1-128 characters drawn
// from [A-Za-z0-9._:-]. The ID is caller-controlled, yet it's echoed in
// the response, written to every log line, and forwarded to downstream
// services — so anything oversized or containing other characters is
// replaced with a freshly generated ID rather than trusted.
func ValidRequestID(s string) bool {
	if len(s) == 0 || len(s) > maxRequestIDLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.', c == '_', c == ':', c == '-':
		default:
			return false
		}
	}
	return true
}
