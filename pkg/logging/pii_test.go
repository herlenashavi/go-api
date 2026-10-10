package logging_test

import (
	"bytes"
	"context"
	"log/slog"
	"regexp"
	"strings"
	"testing"

	"github.com/herlenashavi/go-api/pkg/logging"
)

func newTestLogger(t *testing.T) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	return logging.New(logging.Config{Level: "debug", Output: &buf}), &buf
}

// TestPII_MessageTextIsScanned proves the redaction isn't limited to
// attribute values — PII embedded in the free-text message itself (the
// case key-based redaction can never catch, since there's no key) is
// still caught by pattern matching.
func TestPII_MessageTextIsScanned(t *testing.T) {
	logger, buf := newTestLogger(t)
	logger.Info("failed to notify user at alice@example.com")

	lines := decodeLines(t, buf)
	msg := lines[0]["msg"].(string)
	if strings.Contains(msg, "alice@example.com") {
		t.Fatalf("message still contains the email: %q", msg)
	}
	if !strings.Contains(msg, "[REDACTED_EMAIL]") {
		t.Fatalf("message = %q, want a redaction marker", msg)
	}
}

func TestPII_EmailInAttributeValue(t *testing.T) {
	logger, buf := newTestLogger(t)
	logger.Info("user signed up", "contact", "bob@example.org")

	lines := decodeLines(t, buf)
	got := lines[0]["contact"].(string)
	if strings.Contains(got, "@") {
		t.Fatalf("contact = %q, email was not redacted", got)
	}
}

func TestPII_SSNPattern(t *testing.T) {
	logger, buf := newTestLogger(t)
	logger.Info("verifying identity", "note", "ssn on file: 123-45-6789")

	lines := decodeLines(t, buf)
	got := lines[0]["note"].(string)
	if strings.Contains(got, "123-45-6789") {
		t.Fatalf("note = %q, SSN was not redacted", got)
	}
	if !strings.Contains(got, "[REDACTED_SSN]") {
		t.Fatalf("note = %q, want SSN redaction marker", got)
	}
}

func TestPII_CreditCardValidLuhnIsRedacted(t *testing.T) {
	logger, buf := newTestLogger(t)
	// 4111111111111111 is the well-known Visa test number; it passes Luhn.
	logger.Info("payment attempt", "raw", "card 4111 1111 1111 1111 declined")

	lines := decodeLines(t, buf)
	got := lines[0]["raw"].(string)
	if strings.Contains(got, "4111") {
		t.Fatalf("raw = %q, card number was not redacted", got)
	}
	if !strings.Contains(got, "[REDACTED_CREDIT_CARD]") {
		t.Fatalf("raw = %q, want credit card redaction marker", got)
	}
}

// TestPII_LongNumberFailingLuhnIsNotFlaggedAsCard is the precision check
// for credit-card detection: without the Luhn validation, "any 13-19
// digit run" would flag this ordinary-looking order/tracking number too,
// destroying useful log content for no privacy benefit.
func TestPII_LongNumberFailingLuhnIsNotFlaggedAsCard(t *testing.T) {
	logger, buf := newTestLogger(t)
	logger.Info("order processed", "orderId", "1234567890123456") // fails Luhn

	lines := decodeLines(t, buf)
	got := lines[0]["orderId"].(string)
	if got != "1234567890123456" {
		t.Fatalf("orderId = %q, want unchanged (fails Luhn, should not be treated as a card number)", got)
	}
}

func TestPII_PhoneNumber(t *testing.T) {
	logger, buf := newTestLogger(t)
	logger.Info("sms sent", "detail", "sent to 415-555-0199 successfully")

	lines := decodeLines(t, buf)
	got := lines[0]["detail"].(string)
	if strings.Contains(got, "415-555-0199") {
		t.Fatalf("detail = %q, phone number was not redacted", got)
	}
}

func TestPII_IPv4Address(t *testing.T) {
	logger, buf := newTestLogger(t)
	logger.Info("request from", "detail", "client at 203.0.113.42 connected")

	lines := decodeLines(t, buf)
	got := lines[0]["detail"].(string)
	if strings.Contains(got, "203.0.113.42") {
		t.Fatalf("detail = %q, IPv4 address was not redacted", got)
	}
}

// TestPII_SensitiveKeyRedactedRegardlessOfValueShape proves key-based
// redaction doesn't depend on the value *looking* like anything in
// particular — a field literally named "password" is masked even when
// its value has no recognizable pattern at all.
func TestPII_SensitiveKeyRedactedRegardlessOfValueShape(t *testing.T) {
	cases := []struct{ key string }{
		{"password"}, {"email"}, {"ssn"}, {"creditCard"}, {"credit_card"},
		{"EMAIL_ADDRESS"}, {"firstName"}, {"dateOfBirth"}, {"token"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			logger, buf := newTestLogger(t)
			logger.Info("event", tc.key, "not-actually-pii-shaped-value")

			lines := decodeLines(t, buf)
			if lines[0][tc.key] != "[REDACTED]" {
				t.Fatalf("%s = %v, want fully redacted", tc.key, lines[0][tc.key])
			}
		})
	}
}

// TestPII_GenericKeyNamesAreNotBlanketRedacted confirms the deliberate
// conservatism documented on sensitiveFieldNames: common, mostly-non-PII
// generic key names are not redacted just because of their name — only
// an actually PII-shaped *value* triggers pattern-based redaction.
func TestPII_GenericKeyNamesAreNotBlanketRedacted(t *testing.T) {
	logger, buf := newTestLogger(t)
	logger.Info("startup", "name", "heroes-example", "ip", "not-an-ip-just-a-label")

	lines := decodeLines(t, buf)
	if lines[0]["name"] != "heroes-example" {
		t.Fatalf(`name = %v, want unchanged ("name" alone is not treated as PII)`, lines[0]["name"])
	}
	if lines[0]["ip"] != "not-an-ip-just-a-label" {
		t.Fatalf(`ip = %v, want unchanged (value isn't IP-shaped)`, lines[0]["ip"])
	}
}

// TestPII_IPShapedValueRedactedEvenUnderAGenericKey is the flip side:
// "ip" the key name isn't blanket-redacted, but an actual IP address
// logged under it (or any other key) is still caught by the pattern.
func TestPII_IPShapedValueRedactedEvenUnderAGenericKey(t *testing.T) {
	logger, buf := newTestLogger(t)
	logger.Info("startup", "bindAddr", "192.168.1.1")

	lines := decodeLines(t, buf)
	if strings.Contains(lines[0]["bindAddr"].(string), "192.168.1.1") {
		t.Fatalf("bindAddr = %v, IP-shaped value should be redacted regardless of key name", lines[0]["bindAddr"])
	}
}

func TestPII_NestedGroupAttrs(t *testing.T) {
	logger, buf := newTestLogger(t)
	logger.Info("event", slog.Group("user", slog.String("email", "carol@example.com"), slog.String("id", "u-1")))

	lines := decodeLines(t, buf)
	group, ok := lines[0]["user"].(map[string]any)
	if !ok {
		t.Fatalf("expected a nested group, got %+v", lines[0])
	}
	if group["email"] != "[REDACTED]" {
		t.Fatalf("user.email = %v, want fully redacted (matches sensitive key)", group["email"])
	}
	if group["id"] != "u-1" {
		t.Fatalf("user.id = %v, want unchanged", group["id"])
	}
}

// TestPII_MapAnyValueRedactedByKeyAndPattern exercises the
// slog.Any(map[string]any{...}) path — the shape middleware.AccessLog's
// request-body logging produces — confirming both key-based and
// pattern-based redaction apply recursively inside it, not just to
// top-level string attributes.
func TestPII_MapAnyValueRedactedByKeyAndPattern(t *testing.T) {
	logger, buf := newTestLogger(t)
	body := map[string]any{
		"username": "dave",
		"password": "hunter2",
		"profile": map[string]any{
			"bio": "reach me at dave@example.com anytime",
		},
	}
	logger.Debug("request body", "body", body)

	lines := decodeLines(t, buf)
	got, ok := lines[0]["body"].(map[string]any)
	if !ok {
		t.Fatalf("expected body to decode as an object, got %+v", lines[0]["body"])
	}
	if got["username"] != "dave" {
		t.Fatalf("username = %v, want unchanged", got["username"])
	}
	if got["password"] != "[REDACTED]" {
		t.Fatalf("password = %v, want fully redacted", got["password"])
	}
	profile, ok := got["profile"].(map[string]any)
	if !ok {
		t.Fatalf("profile = %+v", got["profile"])
	}
	bio := profile["bio"].(string)
	if strings.Contains(bio, "@") {
		t.Fatalf("bio = %q, embedded email was not redacted", bio)
	}
}

func TestPII_LogValuerIsResolvedAndRedacted(t *testing.T) {
	logger, buf := newTestLogger(t)
	logger.Info("event", "contact", emailValuer{email: "erin@example.com"})

	lines := decodeLines(t, buf)
	got := lines[0]["contact"].(string)
	if strings.Contains(got, "@") {
		t.Fatalf("contact = %q, LogValuer's resolved email was not redacted", got)
	}
}

type emailValuer struct{ email string }

func (e emailValuer) LogValue() slog.Value { return slog.StringValue(e.email) }

func TestPII_WithAttrsAndWithGroupRedactToo(t *testing.T) {
	logger, buf := newTestLogger(t)
	logger.With("password", "leaked-if-this-fails").WithGroup("req").Info("done", "ssn", "987-65-4321")

	lines := decodeLines(t, buf)
	line := lines[0]
	if line["password"] != "[REDACTED]" {
		t.Fatalf("password (bound via With) = %v, want redacted", line["password"])
	}
	req, ok := line["req"].(map[string]any)
	if !ok {
		t.Fatalf("expected a req group, got %+v", line)
	}
	if req["ssn"] != "[REDACTED]" {
		t.Fatalf("req.ssn = %v, want redacted (field name match)", req["ssn"])
	}
}

func TestRegisterPIIPattern_CustomPattern(t *testing.T) {
	logging.RegisterPIIPattern("employee_id", regexp.MustCompile(`EMP-\d{6}`), nil)

	logger, buf := newTestLogger(t)
	logger.Info("lookup", "note", "found EMP-123456 in the directory")

	lines := decodeLines(t, buf)
	got := lines[0]["note"].(string)
	if strings.Contains(got, "EMP-123456") {
		t.Fatalf("note = %q, custom pattern was not applied", got)
	}
	if !strings.Contains(got, "[REDACTED_EMPLOYEE_ID]") {
		t.Fatalf("note = %q, want custom redaction marker", got)
	}
}

func TestRegisterSensitiveField_CustomField(t *testing.T) {
	logging.RegisterSensitiveField("internalNotes")

	logger, buf := newTestLogger(t)
	logger.Info("event", "internalNotes", "anything at all")

	lines := decodeLines(t, buf)
	if lines[0]["internalNotes"] != "[REDACTED]" {
		t.Fatalf("internalNotes = %v, want redacted after RegisterSensitiveField", lines[0]["internalNotes"])
	}
}

func TestPII_NonStringKindsPassThroughUnredacted(t *testing.T) {
	logger, buf := newTestLogger(t)
	ctx := context.Background()
	logger.InfoContext(ctx, "metrics", "count", 42, "ratio", 3.14, "ok", true)

	lines := decodeLines(t, buf)
	if lines[0]["count"] != float64(42) || lines[0]["ratio"] != 3.14 || lines[0]["ok"] != true {
		t.Fatalf("non-string attrs were altered: %+v", lines[0])
	}
}
