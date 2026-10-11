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

// TestPII_LuhnValidNonCardNumbersAreNotFlagged is the second half of the
// credit-card precision story. Luhn alone passes roughly one in ten
// arbitrary digit runs, so "13-19 digits + Luhn" still caught a steady
// trickle of real identifiers. Every value here passes Luhn and is not a
// number any card issuer could have issued — wrong length for its leading
// digits, or a leading digit no issuer uses at all.
func TestPII_LuhnValidNonCardNumbersAreNotFlagged(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		// Luhn-valid, but 7/8/9 are not an issuer prefix at any length.
		{"order id", "7992739800002"},
		{"account ref", "9000000000000001"},
		// Luhn-valid 16 digits starting 17 — a microsecond epoch timestamp,
		// not a Mastercard 2-series (2221-2720) or any other issuer.
		{"epoch micros", "1760000000000008"},
		// Luhn-valid and starts with 4 (Visa), but Visa mints 13/16/19
		// digits — never 17.
		{"17-digit visa-prefixed id", "40000000000000006"},
	}
	// That each fixture really does pass Luhn — and so is excluded by the
	// IIN filtering rather than by the checksum — is asserted in
	// pii_internal_test.go, which can reach the unexported validators.
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logger, buf := newTestLogger(t)
			logger.Info("processed", "ref", tc.value)

			lines := decodeLines(t, buf)
			if got := lines[0]["ref"].(string); got != tc.value {
				t.Fatalf("ref = %q, want unchanged (%q): Luhn-valid but not a plausible card number",
					got, tc.value)
			}
		})
	}
}

// TestPII_RealCardNumbersStillRedacted is the recall check for the IIN
// filtering above — tightening precision must not start letting actual
// card numbers through. These are the standard per-brand test numbers.
func TestPII_RealCardNumbersStillRedacted(t *testing.T) {
	cases := []struct{ brand, number string }{
		{"visa-16", "4111111111111111"},
		{"visa-13", "4222222222222"},
		{"mastercard", "5555555555554444"},
		{"mastercard-2series", "2223003122003222"},
		{"amex", "378282246310005"},
		{"discover", "6011111111111117"},
		{"diners", "30569309025904"},
		{"jcb", "3530111333300000"},
		{"unionpay", "6200000000000005"},
	}
	for _, tc := range cases {
		t.Run(tc.brand, func(t *testing.T) {
			logger, buf := newTestLogger(t)
			logger.Info("payment", "raw", tc.number)

			lines := decodeLines(t, buf)
			if got := lines[0]["raw"].(string); got != "[REDACTED_CREDIT_CARD]" {
				t.Fatalf("raw = %q, want redacted: %s is a real issuer number", got, tc.brand)
			}
		})
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
// "ip" the key name isn't blanket-redacted, but a routable IP address
// logged under an ordinary key is still caught by the pattern. The key
// here is deliberately not one of pii.go's operational field names, and
// the value deliberately public — the two exemptions this asserts
// *around* are covered by TestPII_InfrastructureAddressesAreNotRedacted
// and TestPII_OperationalKeysSkipPatternRedaction below.
func TestPII_IPShapedValueRedactedEvenUnderAGenericKey(t *testing.T) {
	logger, buf := newTestLogger(t)
	logger.Info("request received", "detail", "forwarded for 203.0.113.42")

	lines := decodeLines(t, buf)
	got := lines[0]["detail"].(string)
	if strings.Contains(got, "203.0.113.42") {
		t.Fatalf("detail = %q, routable IP should be redacted regardless of key name", got)
	}
	if !strings.Contains(got, "[REDACTED_IPV4]") {
		t.Fatalf("detail = %q, want an IPv4 redaction marker", got)
	}
}

// TestPII_InfrastructureAddressesAreNotRedacted pins the false-positive
// fix for ipv4RE: a loopback, unspecified, private, or carrier-NAT
// address is infrastructure by definition and identifies nobody, so
// masking it only costs operators the ability to see what a process
// bound to or which pod called it. These are the addresses go-api's own
// framework code logs — app.New's "listening" line, grpcx's per-RPC
// "peer", a DSN's host — so before this they were all unreadable.
func TestPII_InfrastructureAddressesAreNotRedacted(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value string
	}{
		{"loopback listen address", "detail", "bound to 127.0.0.1:8080"},
		{"unspecified bind address", "detail", "bound to 0.0.0.0:9091"},
		{"rfc1918 class A", "detail", "pod at 10.244.1.7:41234"},
		{"rfc1918 class B", "detail", "node at 172.16.0.9"},
		{"rfc1918 class C", "detail", "gateway at 192.168.1.1"},
		{"carrier-grade NAT", "detail", "cluster ip 100.64.3.21"},
		{"dsn host", "detail", "postgres://svc@10.0.0.5:5432/app"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logger, buf := newTestLogger(t)
			logger.Info("startup", tc.key, tc.value)

			lines := decodeLines(t, buf)
			if got := lines[0][tc.key].(string); got != tc.value {
				t.Fatalf("%s = %q, want unchanged (%q): infrastructure addresses are not personal data",
					tc.key, got, tc.value)
			}
		})
	}
}

// TestPII_OperationalKeysSkipPatternRedaction covers the keys that name
// infrastructure (see pii.go's operationalFieldNames) — needed on top of
// the range check above for a service that genuinely binds to a public
// address, and for version strings that happen to parse as dotted quads.
func TestPII_OperationalKeysSkipPatternRedaction(t *testing.T) {
	cases := []struct{ key, value string }{
		{"addr", "203.0.113.5:8080"},
		{"listenAddr", "203.0.113.5:8080"},
		{"grpc_addr", "198.51.100.7:9091"},
		{"endpoint", "otel-collector.observability:4317"},
		{"version", "1.2.3.4"},
		{"cidr", "203.0.113.0/24"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			logger, buf := newTestLogger(t)
			logger.Info("startup", tc.key, tc.value)

			lines := decodeLines(t, buf)
			if got := lines[0][tc.key].(string); got != tc.value {
				t.Fatalf("%s = %q, want unchanged (%q)", tc.key, got, tc.value)
			}
		})
	}
}

// TestPII_PeerAddressIsStillRedactedWhenPublic is the deliberate
// asymmetry: keys naming *someone else's* address are absent from
// operationalFieldNames, so a public client address is still masked —
// that one is genuinely personal data. Its private-range counterpart
// (the interservice case) is covered above.
func TestPII_PeerAddressIsStillRedactedWhenPublic(t *testing.T) {
	logger, buf := newTestLogger(t)
	logger.Info("rpc completed", "peer", "203.0.113.42:41234", "remoteAddr", "198.51.100.8:1234")

	lines := decodeLines(t, buf)
	for _, key := range []string{"peer", "remoteAddr"} {
		got := lines[0][key].(string)
		if !strings.Contains(got, "[REDACTED_IPV4]") {
			t.Fatalf("%s = %q, a public client address should still be redacted", key, got)
		}
	}
}

// TestPII_OperationalKeyCannotUnredactASensitiveField proves the
// exemption can't be turned into a hole: key-based redaction runs first,
// so a field that is in both sets stays fully redacted, and a nested
// group under an operational key is still walked.
func TestPII_OperationalKeyCannotUnredactASensitiveField(t *testing.T) {
	logging.RegisterOperationalField("password") // deliberately wrong usage

	logger, buf := newTestLogger(t)
	logger.Info("event",
		"password", "still-secret",
		slog.Group("endpoint", slog.String("email", "frank@example.com")))

	lines := decodeLines(t, buf)
	if lines[0]["password"] != "[REDACTED]" {
		t.Fatalf("password = %v, want still redacted despite the operational registration", lines[0]["password"])
	}
	group, ok := lines[0]["endpoint"].(map[string]any)
	if !ok {
		t.Fatalf("expected a nested group under an operational key, got %+v", lines[0])
	}
	if group["email"] != "[REDACTED]" {
		t.Fatalf("endpoint.email = %v, want redacted (a group under an operational key is still walked)", group["email"])
	}
}

// TestPII_OperationalKeyInsideAMapValue covers the slog.Any
// (map[string]any) path — the shape middleware.AccessLog's request-body
// logging produces — where the same key exemption has to apply, since a
// request body can legitimately carry an infrastructure field (a
// config-update endpoint posting a host or endpoint, say).
func TestPII_OperationalKeyInsideAMapValue(t *testing.T) {
	logger, buf := newTestLogger(t)
	body := map[string]any{
		"host":     "203.0.113.5",
		"endpoint": "collector.internal:4317",
		"password": "hunter2",
		"ownerIp":  "203.0.113.9", // not an operational key: still redacted
	}
	logger.Debug("request body", "body", body)

	lines := decodeLines(t, buf)
	got, ok := lines[0]["body"].(map[string]any)
	if !ok {
		t.Fatalf("body did not decode as an object: %+v", lines[0]["body"])
	}
	if got["host"] != "203.0.113.5" {
		t.Errorf("host = %v, want unchanged under an operational key", got["host"])
	}
	if got["endpoint"] != "collector.internal:4317" {
		t.Errorf("endpoint = %v, want unchanged", got["endpoint"])
	}
	if got["password"] != "[REDACTED]" {
		t.Errorf("password = %v, want fully redacted", got["password"])
	}
	if ip, _ := got["ownerIp"].(string); !strings.Contains(ip, "[REDACTED_IPV4]") {
		t.Errorf("ownerIp = %v, want redacted (not an operational key)", got["ownerIp"])
	}
}

func TestRegisterOperationalField_CustomField(t *testing.T) {
	logging.RegisterOperationalField("shardEndpoint")

	logger, buf := newTestLogger(t)
	logger.Info("routing", "shardEndpoint", "203.0.113.90:7000")

	lines := decodeLines(t, buf)
	if lines[0]["shardEndpoint"] != "203.0.113.90:7000" {
		t.Fatalf("shardEndpoint = %v, want unchanged after RegisterOperationalField", lines[0]["shardEndpoint"])
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
