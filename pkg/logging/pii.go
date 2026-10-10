package logging

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
)

// piiHandler wraps another slog.Handler and redacts secrets/PII from
// every record before it reaches the underlying handler. New builds it
// into every logger's chain unconditionally — see this package's doc
// comment for why that matters: a redaction step a call site has to
// remember to invoke (the way Redact, above, works) gets forgotten; one
// built into the handler chain itself cannot be.
//
// Redaction happens two ways, applied to the log message and every
// attribute value (including inside nested groups, resolved
// LogValuers, and map[string]any/[]any values — the shape a JSON
// request body decodes into):
//
//  1. Key-based: a field whose name matches sensitiveFieldNames (case-
//     and separator-insensitive — "emailAddress", "email_address", and
//     "EMAIL-ADDRESS" all match) is replaced with "[REDACTED]" entirely,
//     regardless of its value.
//  2. Pattern-based: every string value (and the log message itself) is
//     scanned against defaultPIIPatterns and any match is masked in
//     place — this is what catches PII logged under an innocuous key,
//     or embedded in a free-text message, that key-based redaction
//     alone would miss.
//
// Neither mechanism is a guarantee that *no* PII can ever reach a log
// line — see this package's doc on RegisterPIIPattern for the honest
// limit of what a regex can catch (a bare name like "John Smith" has no
// detectable pattern). It is a real, defense-in-depth backstop against
// the common, structurally-recognizable cases: emails, phone numbers,
// national ID numbers, payment card numbers, IP addresses, and known
// secret/PII field names — plus whatever you extend it with.
type piiHandler struct {
	next slog.Handler
}

func (h *piiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *piiHandler) Handle(ctx context.Context, record slog.Record) error {
	redacted := slog.NewRecord(record.Time, record.Level, redactString(record.Message), record.PC)
	record.Attrs(func(a slog.Attr) bool {
		redacted.AddAttrs(redactAttr(a))
		return true
	})
	return h.next.Handle(ctx, redacted)
}

func (h *piiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	redacted := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		redacted[i] = redactAttr(a)
	}
	return &piiHandler{next: h.next.WithAttrs(redacted)}
}

func (h *piiHandler) WithGroup(name string) slog.Handler {
	return &piiHandler{next: h.next.WithGroup(name)}
}

func redactAttr(a slog.Attr) slog.Attr {
	if isSensitiveField(a.Key) {
		return slog.String(a.Key, "[REDACTED]")
	}
	return slog.Attr{Key: a.Key, Value: redactValue(a.Value)}
}

func redactValue(v slog.Value) slog.Value {
	v = v.Resolve() // LogValuer -> its underlying value; safe to call on any Value
	switch v.Kind() {
	case slog.KindString:
		return slog.StringValue(redactString(v.String()))
	case slog.KindGroup:
		group := v.Group()
		redacted := make([]slog.Attr, len(group))
		for i, a := range group {
			redacted[i] = redactAttr(a)
		}
		return slog.GroupValue(redacted...)
	case slog.KindAny:
		return slog.AnyValue(redactAny(v.Any()))
	default:
		// Bool/Duration/Float64/Int64/Time/Uint64: none of these are a
		// shape pattern-matching applies to, and none can carry a
		// sensitive *key* (that's already handled above, on the Attr,
		// before we ever get a bare Value) — nothing to redact.
		return v
	}
}

// redactAny handles the slog.KindAny case: a value logged via slog.Any,
// most commonly the result of decoding a JSON body into map[string]any
// (see middleware.AccessLog's request-body logging) or a []any. Anything
// else (a custom struct, say) passes through unchanged — see
// RegisterPIIPattern's doc comment on this limitation.
func redactAny(v any) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, vv := range val {
			if isSensitiveField(k) {
				out[k] = "[REDACTED]"
				continue
			}
			out[k] = redactAny(vv)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, vv := range val {
			out[i] = redactAny(vv)
		}
		return out
	case string:
		return redactString(val)
	case fmt.Stringer:
		return redactString(val.String())
	default:
		return val
	}
}

// --- key-based redaction -----------------------------------------------

// sensitiveFieldNames are field names redacted *entirely*, regardless of
// value, by piiHandler — a superset of the narrower secretKeys above
// (secrets belong here too: nothing should be able to log a password by
// naming the field something logging.RedactKeys was never told about),
// plus common PII field names. Keys are stored pre-normalized (lowercase,
// letters/digits only) so lookups via normalizeField never need to
// normalize the stored side.
//
// This list is deliberately conservative about generic names: bare
// "name" or "ip" are NOT included, because they're extremely common
// non-PII field names ("service name", "bind ip") and blanket-redacting
// them would destroy far more legitimate debugging value than the PII
// they'd occasionally catch — an actual IP-shaped *value* is still
// caught by the ipv4 pattern below regardless of what key it's under.
// More specific compound names (emailAddress, firstName, ...) are
// unambiguous enough to redact outright.
var sensitiveFieldNames = newFieldSet(
	// secrets (mirrors secretKeys above, normalized, plus a few more)
	"password", "secret", "token", "authorization", "apikey", "accesstoken",
	"refreshtoken", "clientsecret", "privatekey", "sessionid", "cookie",

	// PII
	"email", "emailaddress",
	"phone", "phonenumber", "mobilenumber", "mobile", "telephone",
	"ssn", "socialsecuritynumber",
	"dob", "dateofbirth", "birthdate",
	"creditcard", "cardnumber", "ccnumber", "cvv", "cvc",
	"address", "streetaddress", "homeaddress", "mailingaddress", "postaladdress",
	"firstname", "lastname", "fullname", "middlename", "maidenname",
	"passport", "passportnumber",
	"nationalid", "nationalidnumber",
	"driverlicense", "driverlicensenumber",
	"ipaddress",
	"taxid", "ein",
	"bankaccount", "bankaccountnumber", "routingnumber", "iban",
)

type fieldSet struct {
	mu  sync.RWMutex
	set map[string]struct{}
}

func newFieldSet(names ...string) *fieldSet {
	fs := &fieldSet{set: make(map[string]struct{}, len(names))}
	fs.add(names...)
	return fs
}

func (fs *fieldSet) add(names ...string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	for _, n := range names {
		fs.set[normalizeField(n)] = struct{}{}
	}
}

func (fs *fieldSet) has(name string) bool {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	_, ok := fs.set[normalizeField(name)]
	return ok
}

// normalizeField lowercases name and strips everything but letters and
// digits, so "Email_Address", "emailAddress", and "email-address" all
// compare equal.
func normalizeField(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		}
		// everything else (_, -, space, ...) is a separator: dropped.
	}
	return b.String()
}

func isSensitiveField(key string) bool {
	return sensitiveFieldNames.has(key)
}

// RegisterSensitiveField extends piiHandler's key-based redaction with
// additional field names (any separator style — "api-key" and "apiKey"
// both work). Call it once at startup, not per-request; it mutates
// shared state.
func RegisterSensitiveField(names ...string) {
	sensitiveFieldNames.add(names...)
}

// --- pattern-based redaction --------------------------------------------

// piiPattern is one regex-detectable PII shape. validate, if set, is an
// extra check beyond the regex match — e.g. a Luhn checksum for credit
// card numbers, without which "any 13-19 digit run" would flag a huge
// number of ordinary numeric IDs, order numbers, and phone numbers as
// credit cards.
type piiPattern struct {
	name     string
	re       *regexp.Regexp
	validate func(match string) bool
}

var (
	emailRE      = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)
	ssnRE        = regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`)
	creditCardRE = regexp.MustCompile(`\b(?:\d[ -]?){13,19}\b`)
	// Requires a separator (space/dot/dash) between the area, exchange,
	// and number groups — deliberately: an *optional* separator there
	// would also match plain, unseparated 10+ digit runs (order IDs,
	// tracking numbers, ...) as if they were phone numbers. The cost is
	// missing a phone number logged with zero punctuation at all
	// ("4155550199"); that trade favors not mangling ordinary numeric
	// IDs over catching every possible phone format.
	phoneRE = regexp.MustCompile(`(?:\+?1[\s.\-])?\(?\d{3}\)?[\s.\-]\d{3}[\s.\-]\d{4}\b`)
	ipv4RE  = regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\b`)
)

var patternsMu sync.RWMutex
var defaultPIIPatterns = []piiPattern{
	{name: "email", re: emailRE},
	{name: "ssn", re: ssnRE},
	{name: "credit_card", re: creditCardRE, validate: luhnValid},
	{name: "phone", re: phoneRE},
	{name: "ipv4", re: ipv4RE},
}

// RegisterPIIPattern adds a custom pattern to the set piiHandler scans
// every logged string and message against. validate may be nil; pass one
// when the regex alone over-matches (see credit-card detection's Luhn
// check for the canonical example) and you want to reject matches that
// fail an extra check instead of masking them anyway.
//
// This — like the whole pattern-based half of redaction — is
// fundamentally best-effort: a regex can only catch PII that has a
// recognizable *shape* (an email has an @, a phone number has roughly
// the right digit count in roughly the right places). A person's name
// embedded in free text ("contacted by John Smith") has no such shape;
// nothing here, or realistically achievable with a regex at all, catches
// that case. Treat this as a real, valuable backstop against the common
// structured cases, not a certification that no PII can reach a log
// line — the actual guarantee is "don't put PII in a log message in the
// first place," which no amount of filtering replaces.
//
// Call this once at startup, not per-request; it mutates shared state.
func RegisterPIIPattern(name string, re *regexp.Regexp, validate func(match string) bool) {
	patternsMu.Lock()
	defer patternsMu.Unlock()
	defaultPIIPatterns = append(defaultPIIPatterns, piiPattern{name: name, re: re, validate: validate})
}

func redactString(s string) string {
	patternsMu.RLock()
	patterns := defaultPIIPatterns
	patternsMu.RUnlock()

	for _, p := range patterns {
		s = p.re.ReplaceAllStringFunc(s, func(match string) string {
			if p.validate != nil && !p.validate(match) {
				return match
			}
			return "[REDACTED_" + strings.ToUpper(p.name) + "]"
		})
	}
	return s
}

// luhnValid reports whether the digits in s (any non-digit characters —
// spaces, dashes — are ignored) pass the Luhn checksum real payment card
// numbers satisfy, so creditCardRE's otherwise-overbroad "13-19 digits"
// match doesn't flag arbitrary long numeric IDs, order numbers, or
// timestamps as card numbers.
func luhnValid(s string) bool {
	var digits []int
	for _, r := range s {
		if r >= '0' && r <= '9' {
			digits = append(digits, int(r-'0'))
		}
	}
	if len(digits) < 13 || len(digits) > 19 {
		return false
	}

	sum := 0
	parity := len(digits) % 2
	for i, d := range digits {
		if i%2 == parity {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return sum%10 == 0
}
