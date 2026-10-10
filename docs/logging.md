# Logging and PII redaction (`pkg/logging`)

Every go-api service logs structured JSON, unconditionally, and every log
line — regardless of which package or handler wrote it — passes through a
PII/secret-redacting handler before it's encoded. Neither of those is
configurable per se: there's no "pretty text for local dev" mode, and no
way to construct a `logging.New` logger that skips redaction. That's
deliberate — see "Why built-in, not opt-in" below.

## JSON, always

```go
logger := logging.New(logging.Config{
    Level:       "info", // debug|info|warn|error
    ServiceName: "heroes",
    Env:         "prod",
})
logger.Info("hero created", "heroId", id)
// {"time":"...","level":"INFO","msg":"hero created","heroId":"...","service":"heroes","env":"prod"}
```

Earlier versions of this framework switched to a human-readable text
handler whenever a service ran with `Debug: true` (which `examples/heroes`
and `examples/villains` both default to). That's gone: `pkg/app` always
builds a JSON logger now, in every mode, so what you see locally is the
same shape your log aggregator sees in production — no surprises from a
format that only exists in dev.

## PII redaction

Two independent mechanisms, both applied to **the log message and every
attribute value** — including inside nested `slog.Group`s, resolved
`LogValuer`s, and `map[string]any`/`[]any` values (the shape a decoded
JSON body takes):

1. **Key-based.** A field whose name matches a known-sensitive name —
   `password`, `email`, `ssn`, `creditCard`, `firstName`, `dateOfBirth`,
   and others (see `pkg/logging/pii.go`'s `sensitiveFieldNames` for the
   full list) — is replaced with `"[REDACTED]"` entirely, regardless of
   its value. Matching ignores case and separators: `emailAddress`,
   `email_address`, and `EMAIL-ADDRESS` all match.

   Deliberately *not* included: bare `name` or `ip`. Those are extremely
   common, mostly non-PII field names (`"service name"`, `"bind ip"`),
   and blanket-redacting them would destroy far more debugging value
   than the occasional PII they'd catch. An actual IP-shaped *value* is
   still caught by pattern matching below regardless of which key it's
   under.

2. **Pattern-based.** Every string is scanned for emails, US SSNs
   (`123-45-6789` form), phone numbers, IPv4 addresses, and payment card
   numbers — the last validated against a Luhn checksum so an ordinary
   16-digit order ID or tracking number isn't flagged just for being the
   right length. A match is masked in place with a marker naming what was
   found (`[REDACTED_EMAIL]`, `[REDACTED_CREDIT_CARD]`, ...), not deleted
   outright, so the rest of the message stays readable:

   ```
   "contact backup at [REDACTED_EMAIL] if needed"
   ```

   This is what catches PII logged under an innocuous key, or embedded in
   a free-text message, that key-based redaction alone would miss — the
   realistic common case, since nobody names a field `email` and *also*
   writes "oh and by the way the user's email is..." in a log message
   under a field called `note`.

### Extending it

```go
import "regexp"

// Add a field name to redact entirely, regardless of value.
logging.RegisterSensitiveField("internalCaseNumber")

// Add a custom pattern. validate may be nil; pass one when the regex
// alone over-matches (see the credit-card Luhn check for why).
logging.RegisterPIIPattern("employee_id", regexp.MustCompile(`EMP-\d{6}`), nil)
```

Call both once at startup — they mutate package-level state, the same
convention `logging.RedactKeys` (below) already uses.

### Why built-in, not opt-in

The framework already had a narrower redaction helper —
`logging.Redact(v any)` — used by `middleware.AccessLog`'s optional
request-body logging to strip known-sensitive keys from a *parsed JSON
body* before handing it to `slog.Any`. That's still there, unchanged, and
still useful for that specific case (it operates on the decoded
`map[string]any`, not a string slog is about to encode). But it only
protects the one call site that remembers to use it.

The handler in `pii.go` is built into `logging.New`'s chain itself, so
every `logger.Info(...)` anywhere in a service — in framework code, in
your handlers, in a dependency's instrumentation hooks — gets scanned.
Nothing can skip it by forgetting a function call, because there's no
function call to forget.

### The honest limit of what this catches

A regex can only catch PII that has a recognizable **shape**: an email
has an `@`, a phone number has roughly the right digit count in roughly
the right places, a credit card number passes a Luhn check. **A person's
name embedded in free text has no such shape** — `"contacted by John
Smith"` logs exactly as written, same as it would with any other
regex-based filter, because nothing short of an actual NLP/NER model can
reliably recognize "John Smith" as a name versus two ordinary capitalized
words. The `"name":"Clark Kent"` field in this doc's own worked example
below is the honest version of that limitation, not a bug:

```json
{"level":"DEBUG","msg":"request body","body":{"name":"Clark Kent","password":"[REDACTED]","superPower":"contact backup at [REDACTED_EMAIL] if needed"}}
```

Treat this as real, valuable defense-in-depth against the common,
structurally-recognizable cases — not a certification that no PII can
ever reach a log line. The actual guarantee is "don't put PII in a log
message in the first place"; this is the backstop for when that
discipline slips, not a replacement for it.

See `pkg/logging/pii_test.go` for this exercised end-to-end through the
real JSON-encoding pipeline, including the false-positive-avoidance cases
(a 16-digit order ID that fails Luhn is left alone; a bare "name"/"ip"
key is not blanket-redacted).
