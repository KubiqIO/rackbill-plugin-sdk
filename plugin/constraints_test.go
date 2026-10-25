package plugin

import (
	"strings"
	"testing"
)

func i64(v int64) *int64 { return &v }

func TestCheckValue(t *testing.T) {
	length := ConfigField{Key: "pw_len", Label: "Password length", Type: "number", Min: i64(8), Max: i64(20)}
	symbols := ConfigField{Key: "sym", Label: "Symbols", Pattern: `[!_-]*`, MaxLength: 3}
	name := ConfigField{Key: "host", MinLength: 3}
	for _, tc := range []struct {
		f       ConfigField
		value   string
		wantErr string
	}{
		{length, "", ""},
		{length, "8", ""},
		{length, " 20 ", ""},
		{length, "7", "Password length must be 8 to 20"},
		{length, "21", "Password length must be 8 to 20"},
		{length, "12.5", "must be a whole number"},
		{symbols, "!-_", ""},
		{symbols, "!-_!", "at most 3 characters"},
		{symbols, "!$", "must match [!_-]*"},
		{name, "ab", "host must be at least 3 characters"},
		{ConfigField{Key: "free"}, strings.Repeat("x", 500), ""},
		{ConfigField{Key: "bad", Pattern: "("}, "x", "declares an invalid pattern"},
	} {
		err := tc.f.CheckValue(tc.value)
		switch {
		case tc.wantErr == "" && err != nil:
			t.Errorf("%s=%q: unexpected error %v", tc.f.Key, tc.value, err)
		case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
			t.Errorf("%s=%q: error %v, want %q", tc.f.Key, tc.value, err, tc.wantErr)
		}
	}
}

// The pattern is anchored: a partial match is not a match.
func TestCheckValuePatternIsAnchored(t *testing.T) {
	f := ConfigField{Key: "k", Pattern: `[a-z]+`}
	if err := f.CheckValue("abc1"); err == nil {
		t.Fatal("pattern matched a value with a trailing digit")
	}
}

// Constraints survive the wire both ways, including a declared bound of 0,
// which is not the same as no bound.
func TestConstraintsRoundTrip(t *testing.T) {
	in := []ConfigField{{
		Key: "n", Type: "number", Min: i64(0), Max: i64(20),
		MinLength: 1, MaxLength: 5, Pattern: "[0-9]+", Help: "how many",
	}, {Key: "plain"}}
	out := configFieldsFromProto(configFieldsToProto(in))
	if got := out[0]; got.Min == nil || *got.Min != 0 || got.Max == nil || *got.Max != 20 ||
		got.MinLength != 1 || got.MaxLength != 5 || got.Pattern != "[0-9]+" || got.Help != "how many" {
		t.Fatalf("constraints lost on the wire: %+v", got)
	}
	if out[1].Min != nil || out[1].Max != nil {
		t.Fatalf("an undeclared bound came back declared: %+v", out[1])
	}
}

// Sensitive survives the wire, and the debug HTTP log never holds user data.
func TestSensitiveRoundTripAndRedaction(t *testing.T) {
	out := configFieldsFromProto(configFieldsToProto([]ConfigField{{Key: "user_data", Type: "textarea", Sensitive: true}, {Key: "plan"}}))
	if !out[0].Sensitive || out[1].Sensitive {
		t.Fatalf("sensitive lost on the wire: %+v", out)
	}
	for _, body := range []string{
		`{"name":"vps","user_data":"#cloud-config\npassword: hunter2"}`,
		`{"userData":"x","cloud_init":"y","cloudInit":"z"}`,
	} {
		got := RedactSecrets(body)
		if strings.Contains(got, "hunter2") || strings.Contains(got, `"x"`) || strings.Contains(got, `"y"`) || strings.Contains(got, `"z"`) {
			t.Fatalf("user data survived redaction: %s", got)
		}
	}
}
