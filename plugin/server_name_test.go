package plugin

import (
	"fmt"
	"regexp"
	"testing"
)

func TestDefaultServerNameCompatibility(t *testing.T) {
	// Pin the mapping: upgrades must still find guests created by an older
	// plugin, including a create whose response was lost before saving its ref.
	const serviceID = "50841024-27e3-495d-9cbf-9981557e7351"
	const want = "cool-pearl-caribou-4f0756c62786"
	if got := DefaultServerName(serviceID); got != want {
		t.Fatalf("DefaultServerName = %q, want permanent mapping %q", got, want)
	}
	if got := DefaultServerName(""); got != "" {
		t.Fatalf("missing service ID generated %q", got)
	}
	for i, words := range [][]string{serverNameTraits, serverNameColors, serverNameAnimals} {
		if len(words) != 64 {
			t.Fatalf("word list %d has %d entries, want 64", i, len(words))
		}
	}
}

func TestDefaultServerNamesAreDNSLabelsAndUseTheWholeID(t *testing.T) {
	valid := regexp.MustCompile(`^[a-z]+-[a-z]+-[a-z]+-[0-9a-f]{12}$`)
	seen := make(map[string]bool)
	// Services sharing a UUID prefix must still get distinct defaults.
	for i := range 10000 {
		id := fmt.Sprintf("50841024-27e3-495d-9cbf-%012x", i)
		name := DefaultServerName(id)
		// Leave room for provider-specific prefixes inside a 63-byte label.
		if !valid.MatchString(name) || len(name) > 60 {
			t.Fatalf("invalid DNS label %q", name)
		}
		if seen[name] {
			t.Fatalf("duplicate name %q for %s", name, id)
		}
		seen[name] = true
	}
}
