package plugin

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/metadata"
)

func TestLoggingTransportRecordsExchangeAtDebug(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "amount=1000") {
			t.Errorf("server saw body %q", body)
		}
		if r.Header.Get("Authorization") != "Bearer sk_live_abcdefghijklmnop" {
			t.Errorf("server saw authorization %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Request-Id", "req_1")
		w.WriteHeader(201)
		io.WriteString(w, `{"id":"cs_1","client_secret":"cs_1_secret_xyz","amount":1000}`)
	}))
	defer srv.Close()

	buf := captureLog(t, LogDebug)
	c := HTTPClient(5 * time.Second)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/sessions?api_key=k123&page=2", strings.NewReader("amount=1000&currency=usd&api_key=k123"))
	req.Header.Set("Authorization", "Bearer sk_live_abcdefghijklmnop")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 201 || !strings.Contains(string(got), `"cs_1_secret_xyz"`) {
		t.Fatalf("caller saw %d %s — the tap must not alter the body", resp.StatusCode, got)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("want one record per exchange, got %d: %q", len(lines), lines)
	}
	level, msg, kv := parseHclogLine(t, lines[0])
	if level != "debug" {
		t.Fatalf("level = %s", level)
	}
	for _, want := range []string{
		"POST " + srv.URL + "/v1/sessions?api_key=%5Bredacted%5D&page=2 -> 201 Created (",
		"> Authorization: [redacted]",
		"> Content-Type: application/x-www-form-urlencoded",
		"> amount=1000&currency=usd&api_key=[redacted]",
		"< Content-Type: application/json",
		"< Request-Id: req_1",
		`< {"id":"cs_1","client_secret":"[redacted]","amount":1000}`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("record lacks %q:\n%s", want, msg)
		}
	}
	for _, leaked := range []string{"sk_live_abcdefghijklmnop", "k123", "cs_1_secret_xyz"} {
		if strings.Contains(lines[0], leaked) {
			t.Errorf("record leaks %q", leaked)
		}
	}
	if kv["method"] != "POST" || kv["status"] != float64(201) || kv["request_bytes"] != float64(37) || kv["response_bytes"] != float64(61) {
		t.Fatalf("kv = %v", kv)
	}
}

func TestLoggingTransportRedactsBareAPIKey(t *testing.T) {
	const credential = "syntheticNameSiloCredential"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("key") != credential {
			t.Error("redaction changed the request credential")
		}
		io.WriteString(w, "ok")
	}))
	defer srv.Close()
	buf := captureLog(t, LogDebug)
	resp, err := HTTPClient(5 * time.Second).Get(srv.URL + "/api/getDomainInfo?key=" + credential + "&monkey=banana")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	_, msg, fields := parseHclogLine(t, strings.TrimSpace(buf.String()))
	if strings.Contains(buf.String(), credential) {
		t.Fatal("API key appeared in a debug record")
	}
	for _, loggedURL := range []string{msg, fields["url"].(string)} {
		if !strings.Contains(loggedURL, "key=%5Bredacted%5D") || !strings.Contains(loggedURL, "monkey=banana") {
			t.Fatalf("unexpected redacted URL: %s", loggedURL)
		}
	}
}

func TestLoggingTransportWithholdsPanelSessionReplies(t *testing.T) {
	for _, tc := range []struct {
		name, path, contentType, body, secret string
	}{
		{"cpanel", "/json-api/create_user_session", "application/json", `{"metadata":{"result":1},"data":{"url":"https://panel.example:2083/cpsess12345/login/?session=syntheticCpanelSession"}}`, "syntheticCpanelSession"},
		{"directadmin", "/CMD_API_LOGIN_KEYS", "application/x-www-form-urlencoded", `error=0&details=https%3A%2F%2Fpanel.example%3A2222%2FCMD_LOGIN%3Fusername%3Dbuyer%26password%3DsyntheticDASession`, "syntheticDASession"},
		{"plesk", "/enterprise/control/agent.php", "text/xml", `<packet><server><create_session><result><status>ok</status><id>syntheticPleskSession</id></result></create_session></server></packet>`, "syntheticPleskSession"},
		{"plesk-proxy", "/panel/enterprise/control/agent.php", "text/xml", `<packet><server><create_session><result><id>syntheticPleskSession</id></result></create_session></server></packet>`, "syntheticPleskSession"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			buf := captureLog(t, LogDebug)
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+tc.path, strings.NewReader(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := LoggingTransport(nil).RoundTrip(req)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || string(raw) != tc.body {
				t.Fatalf("caller lost the session response: %q %v", raw, err)
			}
			if tap := resp.Body.(*tapReader); tap.cap.buf.Len() != 0 {
				t.Fatal("session response was retained in the diagnostic buffer")
			}
			if strings.Contains(buf.String(), tc.secret) {
				t.Fatal("debug log exposed the session credential")
			}
			_, msg, attrs := parseHclogLine(t, strings.TrimSpace(buf.String()))
			if !strings.Contains(msg, "withheld: panel session reply") || attrs["status"] != float64(200) || attrs["response_bytes"] != float64(len(tc.body)) {
				t.Fatalf("exchange metadata missing: %s %#v", msg, attrs)
			}
		})
	}
}

func TestRedactHistoricalPanelSessions(t *testing.T) {
	for _, input := range []string{
		`{"url":"https://panel.example:2083/cpsess12345/login/?session=syntheticSessionCredential"}`,
		`details=https%3A%2F%2Fpanel.example%3A2222%2FCMD_LOGIN%3Fusername%3Dbuyer%26password%3DsyntheticSessionCredential&error=0`,
		`result=https%3A%2F%2Fpanel.example%3A2222%2FCMD_LOGIN%3Fkey%3DsyntheticSessionCredential&error=0`,
		`details=https%3A%2F%2Fpanel.example%3A2222%2FCMD_LOGIN%3Fkey%3DsyntheticSessionCredential%`,
		`https://panel.example/proxy/CMD_LOGIN?password=syntheticSessionCredential%`,
		`https://panel.example:8443/enterprise/rsession_init.php?PLESKSESSID=syntheticSessionCredential`,
		`<packet><server><create_session><result><status>ok</status><id>syntheticSessionCredential</id></result></create_session></server></packet>`,
		"< <create_session>\n< <result><id>syntheticSessionCredential</id></result>\n< </create_session>",
		`<create_session><result><id>syntheticSessionCredential`,
	} {
		out := RedactSecrets(input)
		if strings.Contains(out, "syntheticSessionCredential") || !strings.Contains(out, "[redacted") {
			t.Errorf("session survived redaction: %s", out)
		}
		if strings.HasSuffix(input, "&error=0") && !strings.HasSuffix(out, "&error=0") {
			t.Errorf("redaction removed unrelated result: %s", out)
		}
	}
	const ordinary = `{"url":"https://panel.example:8443/dashboard?id=123"} details=https%3A%2F%2Fpanel.example%2Fhelp&error=0`
	if got := RedactSecrets(ordinary); got != ordinary {
		t.Fatalf("ordinary URLs changed: %s", got)
	}
}

// A console endpoint's reply is a session credential whose field names give
// nothing away (SolusVM 2's vnc_up answers {"url": "<ticket>"}), so it is
// withheld whole; the caller still reads it untouched.
func TestLoggingTransportWithholdsConsoleReplies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"url":"YjBhYzY2ZGRlMTg5"}`)
	}))
	defer srv.Close()

	buf := captureLog(t, LogDebug)
	c := HTTPClient(5 * time.Second)
	for _, path := range []string{"/api/v1/servers/368/vnc_up", "/api2/json/nodes/pve/qemu/100/vncproxy"} {
		buf.Reset()
		resp, err := c.Post(srv.URL+path, "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(got), "YjBhYzY2ZGRlMTg5") {
			t.Fatalf("caller saw %s: withholding must not alter the body", got)
		}
		if strings.Contains(buf.String(), "YjBhYzY2ZGRlMTg5") || !strings.Contains(buf.String(), "bytes withheld: console session reply") {
			t.Errorf("%s record:\n%s", path, buf.String())
		}
	}
	buf.Reset()
	resp, err := c.Get(srv.URL + "/api/v1/servers/368")
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(buf.String(), "withheld") {
		t.Errorf("an ordinary read was withheld:\n%s", buf.String())
	}
}

func TestLoggingTransportWithholdsPaymentBodies(t *testing.T) {
	const request = `{"payment_source":{"paypal":{"vault_id":"private-vault-request"}},"payment_method":"pm_privateMethod"}`
	const response = `{"payment_source":{"paypal":{"attributes":{"vault":{"id":"private-vault-response","status":"VAULTED"}}}},"customer":"cus_privateCustomer","latest_charge":{"payment_method_details":{"ideal":{"generated_sepa_debit":"pm_privateGenerated","generated_sepa_debit_mandate":"mandate_privateGenerated"}}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) != request {
			t.Error("withholding changed the provider request")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("fail") != "" {
			w.WriteHeader(http.StatusUnprocessableEntity)
		}
		io.WriteString(w, response)
	}))
	defer srv.Close()
	buf := captureLog(t, LogDebug)
	for _, path := range []string{"/v1/checkout/sessions", "/v1/checkout/sessions/cs_paid", "/v1/payment_intents", "/v1/payment_intents/pi_paid", "/v1/payment_intents/pi_paid?expand%5B%5D=latest_charge", "/v1/payment_intents/pi_paid/cancel", "/v2/checkout/orders", "/v2/checkout/orders/order/capture", "/v2/checkout/orders/order?fail=1"} {
		t.Run(path, func(t *testing.T) {
			buf.Reset()
			resp, err := HTTPClient(time.Second).Post(srv.URL+path, "application/json", strings.NewReader(request))
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if string(body) != response {
				t.Fatal("withholding changed the provider response")
			}
			line := strings.TrimSpace(buf.String())
			_, msg, kv := parseHclogLine(t, line)
			if strings.Count(msg, "bytes withheld: payment provider body") != 2 || kv["request_bytes"] != float64(len(request)) || kv["response_bytes"] != float64(len(response)) || kv["status"] != float64(resp.StatusCode) {
				t.Fatalf("payment exchange metadata missing: %s", line)
			}
			for _, secret := range []string{"private-vault-request", "private-vault-response", "pm_privateMethod", "cus_privateCustomer", "pm_privateGenerated", "mandate_privateGenerated"} {
				if strings.Contains(line, secret) {
					t.Fatal("payment reference entered debug log")
				}
			}
		})
	}
	// Withheld bodies count bytes without retaining any plaintext excerpt.
	cap := &capture{withheld: true}
	reader := &tapReader{rc: io.NopCloser(strings.NewReader(response)), cap: cap}
	io.Copy(io.Discard, reader)
	reader.Close()
	if cap.buf.Len() != 0 || cap.total != int64(len(response)) {
		t.Fatal("withheld response was retained in the log capture buffer")
	}
}

type paymentErrorTransport struct{}

func (paymentErrorTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	io.Copy(io.Discard, r.Body)
	return nil, errors.New("unknown provider error echoing private-vault-value")
}

func TestLoggingTransportPaymentErrorOmitsProviderDetails(t *testing.T) {
	buf := captureLog(t, LogDebug)
	c := &http.Client{Transport: LoggingTransport(paymentErrorTransport{})}
	_, err := c.Post("https://example.invalid/v2/checkout/orders", "application/json", strings.NewReader(`{"id":"private-vault-value"}`))
	if err == nil || !strings.Contains(err.Error(), "private-vault-value") {
		t.Fatal("transport must preserve the caller's error")
	}
	if strings.Contains(buf.String(), "private-vault-value") || !strings.Contains(buf.String(), "payment provider transport failed") {
		t.Fatal("payment transport error entered debug log")
	}
}

func TestLoggingTransportSilentAndCheapAboveDebug(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		io.WriteString(w, "ok")
	}))
	defer srv.Close()
	buf := captureLog(t, LogInfo)
	resp, err := HTTPClient(time.Second).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if calls != 1 || buf.Len() != 0 {
		t.Fatalf("calls=%d log=%q", calls, buf.String())
	}
}

func TestLoggingTransportBinaryAndTruncation(t *testing.T) {
	big := strings.Repeat("x", httpLogBodyLimit+100)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bin":
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte{0x89, 'P', 'N', 'G', 0, 1, 2})
		default:
			w.Header().Set("Content-Type", "text/plain")
			io.WriteString(w, big)
		}
	}))
	defer srv.Close()
	buf := captureLog(t, LogDebug)
	c := HTTPClient(time.Second)
	for _, p := range []string{"/bin", "/big"} {
		resp, err := c.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		if b, _ := io.ReadAll(resp.Body); p == "/big" && len(b) != len(big) {
			t.Fatalf("caller got %d bytes of /big", len(b))
		}
		resp.Body.Close()
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("records = %d", len(lines))
	}
	_, bin, _ := parseHclogLine(t, lines[0])
	if !strings.Contains(bin, "<7 bytes of image/png>") {
		t.Errorf("binary body not summarised: %s", bin)
	}
	_, long, _ := parseHclogLine(t, lines[1])
	if !strings.Contains(long, "… (8192 of 8292 bytes shown)") {
		t.Errorf("long body not marked truncated: %s", long[len(long)-80:])
	}
}

func TestLoggingTransportErrors(t *testing.T) {
	buf := captureLog(t, LogDebug)
	c := HTTPClient(time.Second)
	_, err := c.Get("http://127.0.0.1:1/nope")
	if err == nil {
		t.Fatal("expected a connection error")
	}
	_, msg, kv := parseHclogLine(t, strings.TrimSpace(buf.String()))
	if !strings.HasPrefix(msg, "GET http://127.0.0.1:1/nope -> error: ") || kv["error"] == nil {
		t.Fatalf("error record = %q %v", msg, kv)
	}
}

func TestLoggingTransportRedactsEnomCredential(t *testing.T) {
	const token = "private-enom-credential"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("pw") != token {
			t.Error("redaction changed the credential sent to the provider")
		}
		io.WriteString(w, "ok")
	}))
	defer srv.Close()
	buf := captureLog(t, LogDebug)
	resp, err := HTTPClient(time.Second).Get(srv.URL + "/interface.asp?pw=" + token + "&command=check&pw_hint=readable")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if logged := buf.String(); strings.Contains(logged, token) || !strings.Contains(logged, "pw=%5Bredacted%5D") || !strings.Contains(logged, "pw_hint=readable") {
		t.Fatalf("unexpected redacted HTTP log: %s", logged)
	}
}

func TestRedactSecrets(t *testing.T) {
	cases := map[string]string{
		`{"password":"hunter2","user":"bob"}`:                                                  `{"password":"[redacted]","user":"bob"}`,
		`{"api_key": "abc", "Authorization":"Bearer x"}`:                                       `{"api_key":"[redacted]", "Authorization":"[redacted]"}`,
		`<login><password>pw</password><name>n</name></login>`:                                 `<login><password>[redacted]</password><name>n</name></login>`,
		`token=abc&monkey=banana&client_secret=zzz`:                                            `token=[redacted]&monkey=banana&client_secret=[redacted]`,
		`https://www.namesilo.com/api/getDomainInfo?key=credential&domain=example.test`:        `https://www.namesilo.com/api/getDomainInfo?key=[redacted]&domain=example.test`,
		`KEY=credential&monkey=banana`:                                                         `KEY=[redacted]&monkey=banana`,
		`Get "https://reseller.enom.com/interface.asp?pw=private%2Bkey&uid=reseller": timeout`: `Get "https://reseller.enom.com/interface.asp?pw=[redacted]&uid=reseller": timeout`,
		`PW=private&pw_hint=keep&monkey=banana`:                                                `PW=[redacted]&pw_hint=keep&monkey=banana`,
		// Virtualizor's bare "pass": the admin API key in every query string
		// and the root password in VM creation bodies.
		`act=vs&adminapipass=s3cret&api=json`:                                  `act=vs&adminapipass=[redacted]&api=json`,
		`{"rootpass":"toor","user_pass":"pw","hostname":"vm1"}`:                `{"rootpass":"[redacted]","user_pass":"[redacted]","hostname":"vm1"}`,
		`<vps><rootpass>toor</rootpass><ram>512</ram></vps>`:                   `<vps><rootpass>[redacted]</rootpass><ram>512</ram></vps>`,
		"Authorization: Basic dXNlcjpwYXNz\nAccept: */*":                       "Authorization: [redacted]\nAccept: */*",
		`key sk_live_1234567890abcdef used`:                                    `key [redacted] used`,
		"-----BEGIN RSA PRIVATE KEY-----\nMIIE\n-----END RSA PRIVATE KEY-----": "[redacted private key]",
		`plain text with no secrets`:                                           `plain text with no secrets`,
		// Console credentials: SolusVM 2's standing ticket and Proxmox's
		// vncproxy reply and websocket query. Booleans and numbers stay.
		`{"vnc_url":"YjBhYzY2","settings":{"vnc_enabled":true,"vnc_password":"pw"}}`:     `{"vnc_url":"[redacted]","settings":{"vnc_enabled":true,"vnc_password":"[redacted]"}}`,
		`{"ticket":"PVEVNC:abc","port":"5900"}`:                                          `{"ticket":"[redacted]","port":"5900"}`,
		`port=5900&vncticket=PVEVNC:abc`:                                                 `port=5900&vncticket=[redacted]`,
		`{"customer":"cus_123","payment_method":"pm_123","vault_id":"paypal-reference"}`: `{"customer":"[redacted]","payment_method":"[redacted]","vault_id":"[redacted]"}`,
		`customer=cus_123&payment_method=pm_123&vault_id=paypal-reference`:               `customer=[redacted]&payment_method=[redacted]&vault_id=[redacted]`,
		`provider rejected pm_privateMethod for cus_privateCustomer`:                     `provider rejected [redacted] for [redacted]`,
	}
	for in, want := range cases {
		if got := RedactSecrets(in); got != want {
			t.Errorf("RedactSecrets(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestApplyLevelFromContext(t *testing.T) {
	captureLog(t, LogInfo)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(MetadataLogLevel, "debug"))
	applyLevelFromContext(ctx)
	if LogLevel() != LogDebug {
		t.Fatalf("level = %s after metadata debug", LogLevel())
	}
	// Junk in the metadata is ignored, not adopted.
	applyLevelFromContext(metadata.NewIncomingContext(context.Background(), metadata.Pairs(MetadataLogLevel, "loud")))
	if LogLevel() != LogDebug {
		t.Fatalf("level = %s after junk metadata", LogLevel())
	}
	applyLevelFromContext(context.Background())
	if LogLevel() != LogDebug {
		t.Fatalf("level = %s after a call with no metadata", LogLevel())
	}
}
