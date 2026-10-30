package plugin

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// External API calls.
//
// Every HTTP call a plugin makes to the system it integrates — a panel, a
// payment processor, a registrar — must be visible at debug: the URL, the
// request, and the response. It is the one piece of evidence that settles
// "did the plugin ask the right thing" and "what did the far side actually
// say" during validation. HTTPClient and LoggingTransport implement that
// rule; first-party plugins use nothing else, and the plugin design of
// record (§17.1) asks the same of third parties.
//
// Cost when debug is off: one level check per call. At debug, request
// bodies are copied as the transport reads them and response bodies as
// the caller reads them (no extra reads, no buffering of the whole body),
// each capped at httpLogBodyLimit, and the exchange is written as one
// record when the response body is closed — so request and response stay
// together in the ring. Credentials are redacted before anything is
// written: sensitive headers, query parameters, and secret-shaped fields
// in bodies (RedactSecrets). A console endpoint's reply is withheld whole
// (consolePath). Checkout/vault exchanges with reusable payment references
// withhold both bodies without capturing their contents (paymentPath).

// httpLogBodyLimit is how much of each body a debug record keeps.
const httpLogBodyLimit = 8 << 10

// HTTPClient returns an http.Client whose calls are logged at debug.
// timeout is the usual per-call ceiling (zero for none).
func HTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: LoggingTransport(nil)}
}

// LoggingTransport wraps base (http.DefaultTransport when nil) so every
// exchange through it is logged at debug. Use it when the plugin needs its
// own transport — a TLS setting for a self-signed panel, a proxy.
func LoggingTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if _, ok := base.(*loggingTransport); ok {
		return base
	}
	return &loggingTransport{base: base}
}

type loggingTransport struct{ base http.RoundTripper }

func (t *loggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !Log.Enabled(req.Context(), slog.LevelDebug) {
		return t.base.RoundTrip(req)
	}
	x := &exchange{method: req.Method, url: redactURL(req.URL), reqHeader: req.Header.Clone(), start: time.Now()}
	x.paymentBodies = paymentPath(req.URL)
	x.reqBody.withheld, x.respBody.withheld = x.paymentBodies, x.paymentBodies
	x.reqType = req.Header.Get("Content-Type")
	x.reqLen = req.ContentLength
	sent := req
	if req.Body != nil && req.Body != http.NoBody {
		// The transport must not modify the caller's request: send a
		// shallow clone whose body is tapped as the transport reads it.
		sent = req.Clone(req.Context())
		sent.Body = &tapReader{rc: req.Body, cap: &x.reqBody}
	}
	resp, err := t.base.RoundTrip(sent)
	if err != nil {
		x.err = err
		x.emit()
		return nil, err
	}
	x.status = resp.Status
	x.statusCode = resp.StatusCode
	x.respHeader = resp.Header.Clone()
	x.respType = resp.Header.Get("Content-Type")
	x.respLen = resp.ContentLength
	x.withhold = consolePath(req.URL)
	if resp.Body == nil {
		x.emit()
		return resp, nil
	}
	// The record is written when the caller finishes with the body (EOF or
	// Close), so the response excerpt is what the caller actually read.
	resp.Body = &tapReader{rc: resp.Body, cap: &x.respBody, done: x.emit}
	return resp, nil
}

// capture is the first httpLogBodyLimit bytes of a body plus a count.
type capture struct {
	buf      bytes.Buffer
	total    int64
	withheld bool
}

// tapReader copies what passes through it into cap and, when done is set,
// calls it once at EOF or Close.
type tapReader struct {
	rc   io.ReadCloser
	cap  *capture
	done func()
	once sync.Once
}

func (t *tapReader) Read(p []byte) (int, error) {
	n, err := t.rc.Read(p)
	if n > 0 {
		t.cap.total += int64(n)
		if room := httpLogBodyLimit - t.cap.buf.Len(); room > 0 && !t.cap.withheld {
			if n < room {
				room = n
			}
			t.cap.buf.Write(p[:room])
		}
	}
	if err == io.EOF && t.done != nil {
		t.once.Do(t.done)
	}
	return n, err
}

func (t *tapReader) Close() error {
	err := t.rc.Close()
	if t.done != nil {
		t.once.Do(t.done)
	}
	return err
}

type exchange struct {
	method, url   string
	start         time.Time
	reqHeader     http.Header
	reqType       string
	reqLen        int64
	reqBody       capture
	status        string
	statusCode    int
	respHeader    http.Header
	respType      string
	respLen       int64
	respBody      capture
	withhold      bool // a console endpoint: the reply is a session credential
	paymentBodies bool // both bodies may contain reusable references under generic id fields
	err           error
}

// emit writes the exchange as one debug record: a summary line, then the
// request and response with `>` and `<` prefixes.
func (x *exchange) emit() {
	dur := time.Since(x.start)
	var b strings.Builder
	errorMessage := ""
	if x.err != nil {
		errorMessage = RedactSecrets(x.err.Error())
		if x.paymentBodies {
			errorMessage = "payment provider transport failed"
		}
		fmt.Fprintf(&b, "%s %s -> error: %s (%s)\n", x.method, x.url, errorMessage, dur.Round(time.Millisecond))
	} else {
		fmt.Fprintf(&b, "%s %s -> %s (%s)\n", x.method, x.url, x.status, dur.Round(time.Millisecond))
	}
	writeHeaders(&b, "> ", x.reqHeader)
	b.WriteString("> \n")
	if x.paymentBodies {
		fmt.Fprintf(&b, "> <%d bytes withheld: payment provider body>\n", x.reqBody.total)
	} else {
		writeBody(&b, "> ", x.reqType, x.reqLen, &x.reqBody)
	}
	if x.err == nil {
		writeHeaders(&b, "< ", x.respHeader)
		b.WriteString("< \n")
		if x.paymentBodies {
			fmt.Fprintf(&b, "< <%d bytes withheld: payment provider body>\n", x.respBody.total)
		} else if x.withhold {
			fmt.Fprintf(&b, "< <%d bytes withheld: console session reply>\n", x.respBody.total)
		} else {
			writeBody(&b, "< ", x.respType, x.respLen, &x.respBody)
		}
	}
	attrs := []any{
		"method", x.method, "url", x.url, "duration_ms", dur.Milliseconds(),
		"request_bytes", x.reqBody.total,
	}
	if x.err != nil {
		attrs = append(attrs, "error", errorMessage)
	} else {
		attrs = append(attrs, "status", x.statusCode, "response_bytes", x.respBody.total)
	}
	Log.Debug(strings.TrimRight(b.String(), "\n"), attrs...)
}

func writeHeaders(b *strings.Builder, prefix string, h http.Header) {
	names := make([]string, 0, len(h))
	for k := range h {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		v := strings.Join(h[k], ", ")
		if sensitiveHeader(k) {
			v = "[redacted]"
		}
		fmt.Fprintf(b, "%s%s: %s\n", prefix, k, v)
	}
}

func writeBody(b *strings.Builder, prefix, contentType string, declared int64, c *capture) {
	total := c.total
	if total == 0 && declared > 0 {
		total = declared
	}
	if c.buf.Len() == 0 {
		if total > 0 {
			fmt.Fprintf(b, "%s<%d bytes not read>\n", prefix, total)
		} else {
			fmt.Fprintf(b, "%s<no body>\n", prefix)
		}
		return
	}
	if !textual(contentType, c.buf.Bytes()) {
		ct := contentType
		if ct == "" {
			ct = "binary"
		}
		fmt.Fprintf(b, "%s<%d bytes of %s>\n", prefix, total, ct)
		return
	}
	text := RedactSecrets(strings.ToValidUTF8(c.buf.String(), "�"))
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		b.WriteString(prefix)
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if int64(c.buf.Len()) < total {
		fmt.Fprintf(b, "%s… (%d of %d bytes shown)\n", prefix, c.buf.Len(), total)
	}
}

// textual decides whether a body is worth printing: a text-like content
// type, or no declared type and bytes that look like text.
func textual(contentType string, sample []byte) bool {
	mt, _, _ := mime.ParseMediaType(contentType)
	mt = strings.ToLower(mt)
	switch {
	case strings.HasPrefix(mt, "text/"),
		strings.Contains(mt, "json"), strings.Contains(mt, "xml"),
		strings.Contains(mt, "x-www-form-urlencoded"), strings.Contains(mt, "javascript"),
		strings.Contains(mt, "yaml"), strings.Contains(mt, "graphql"):
		return true
	case mt != "":
		return false
	}
	if !utf8.Valid(sample) {
		return false
	}
	for _, r := range string(sample) {
		if r < ' ' && r != '\n' && r != '\r' && r != '\t' {
			return false
		}
	}
	return true
}

// consolePath reports a call that opens a console session. Its reply is a
// credential in whatever shape the far side picks (SolusVM 2's vnc_up
// answers {"url": "<ticket>"}, which no field name gives away), so the body
// is withheld whole rather than redacted by name.
func consolePath(u *url.URL) bool {
	if u == nil {
		return false
	}
	p := strings.ToLower(u.Path)
	last := p[strings.LastIndex(p, "/")+1:]
	for _, w := range []string{"vnc", "console", "spice", "termproxy"} {
		if strings.Contains(last, w) {
			return true
		}
	}
	return false
}

// Stripe and PayPal place reusable payment references in fields such as id,
// including nested error responses. Withhold both bodies for the complete
// checkout/intent resource family, including provider-compatible test endpoints.
// Path matching avoids relying on configurable API hosts to preserve privacy.
func paymentPath(u *url.URL) bool {
	if u == nil {
		return false
	}
	p := strings.ToLower(u.Path)
	for _, prefix := range []string{"/v1/checkout/sessions", "/v1/payment_intents", "/v2/checkout/orders"} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}

var sensitiveHeaderWords = []string{
	"auth", "token", "secret", "password", "passwd", "pass", "cookie", "key", "signature", "credential", "vault", "payment-method", "payment_method", "customer",
}

func sensitiveHeader(name string) bool {
	n := strings.ToLower(name)
	for _, w := range sensitiveHeaderWords {
		if strings.Contains(n, w) {
			return true
		}
	}
	return false
}

// "pass" on its own is deliberate: Virtualizor sends adminapipass in every
// query string and rootpass in VM creation bodies, and the word is too short
// to be safe to leave out. A parameter named bypass or passport is redacted
// with it — over-redaction in a debug log costs nothing.
// NameSilo sends its API credential as the exact query name "key". Match
// that name without treating ordinary names such as "monkey" as secrets.
var sensitiveParamRe = regexp.MustCompile(`(?i)(^key$|password|passwd|pass|secret|token|api[-_]?key|apikey|access[-_]?key|private[-_]?key|client[-_]?secret|signature|sig|auth|credential|ticket|payment[-_]?method|vault|customer)`)

// redactURL masks userinfo and secret-shaped query values.
func redactURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	cp := *u
	if cp.User != nil {
		cp.User = url.User(cp.User.Username() + ":[redacted]")
		if _, has := u.User.Password(); !has {
			cp.User = url.User("[redacted]")
		}
	}
	if cp.RawQuery != "" {
		q := cp.Query()
		changed := false
		for k := range q {
			if sensitiveParamRe.MatchString(k) {
				q[k] = []string{"[redacted]"}
				changed = true
			}
		}
		if changed {
			cp.RawQuery = q.Encode()
		}
	}
	return cp.String()
}

// Secret-shaped text, in bodies and free text. Applied to every body
// excerpt before it is logged, and again by the agent over everything a
// bug report carries.
var secretPatterns = []struct {
	re   *regexp.Regexp
	repl string
}{
	// PEM private keys, whole.
	{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`), "[redacted private key]"},
	// JSON: "password": "…" and friends (the key keeps its name). Only
	// string values match, so "vnc_enabled": true stays readable while a
	// console ticket ("vnc_url" on SolusVM 2 servers, Proxmox's "ticket")
	// goes.
	{regexp.MustCompile(`(?i)"([^"]*(?:password|passwd|pass|secret|token|api[-_]?key|apikey|private[-_]?key|authorization|credential|signature|user[-_]?data|cloud[-_]?init|vnc|ticket|payment[-_]?method|vault|customer)[^"]*)"\s*:\s*"(?:[^"\\]|\\.)*"`), `"$1":"[redacted]"`},
	// XML: <password>…</password>.
	{regexp.MustCompile(`(?i)<([\w:.-]*(?:password|passwd|pass|secret|token|apikey|api_key|private_key|credential|payment[-_]?method|vault|customer)[\w:.-]*)>[^<]*</`), "<$1>[redacted]</"},
	// Form, query and env style: password=… (word chars around the key so
	// "monkey=" stays, "api_key=", "x-api-key=" and "adminapipass=" go).
	{regexp.MustCompile(`(?i)\b(key|[\w.-]*(?:password|passwd|pass|secret|token|api[-_]?key|apikey|private[-_]?key|client[-_]?secret|access[-_]?key|signature|credential|ticket|payment[-_]?method|vault|customer)[\w.-]*)=([^&\s"'<>]+)`), "$1=[redacted]"},
	// Header lines and "Authorization: Bearer …" echoed in bodies.
	{regexp.MustCompile(`(?im)^([ \t]*(?:authorization|proxy-authorization|cookie|set-cookie|x-api-key|x-auth-token|x-access-token|x-amz-security-token)[ \t]*:)[ \t]*.*$`), "$1 [redacted]"},
	{regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`), "$1 [redacted]"},
	// Well-known key shapes.
	{regexp.MustCompile(`\b(?:cus|pm)_[A-Za-z0-9]+\b`), "[redacted]"},
	{regexp.MustCompile(`\b(?:sk|rk)_(?:live|test)_[A-Za-z0-9]{8,}\b`), "[redacted]"},
	{regexp.MustCompile(`\bwhsec_[A-Za-z0-9]{8,}\b`), "[redacted]"},
	{regexp.MustCompile(`\bxox[abpr]-[A-Za-z0-9-]{8,}\b`), "[redacted]"},
	{regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`), "[redacted]"},
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), "[redacted]"},
}

// RedactSecrets masks credential-shaped text: JSON, XML and form fields
// whose names say password, secret, token, key, or signature (and, in JSON,
// form and query text, vnc or ticket: console credentials); bearer and
// basic authorization values; PEM private keys; and well-known API key
// formats. Provider payment-method, vault and customer references are also
// sensitive. Names are kept so the shape of a payload stays readable.
func RedactSecrets(s string) string {
	for _, p := range secretPatterns {
		s = p.re.ReplaceAllString(s, p.repl)
	}
	return s
}
