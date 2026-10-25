package plugin

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// CheckValue reports whether value satisfies the field's declared limits
// (MinLength, MaxLength, Pattern, Min, Max). The agent runs it on every save
// that writes the field and a plugin runs it on what it receives, so both
// sides refuse the same values with the same words. An empty value passes:
// whether one is allowed is Required's business. A field that declares no
// limits accepts anything, exactly as before they existed.
func (f ConfigField) CheckValue(value string) error {
	if value == "" {
		return nil
	}
	name := f.Label
	if name == "" {
		name = f.Key
	}
	if f.Min != nil || f.Max != nil {
		n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			return fmt.Errorf("%s must be a whole number", name)
		}
		if (f.Min != nil && n < *f.Min) || (f.Max != nil && n > *f.Max) {
			return fmt.Errorf("%s must be %s", name, f.LimitsHint())
		}
	}
	if f.MinLength > 0 || f.MaxLength > 0 {
		n := utf8.RuneCountInString(value)
		if (f.MinLength > 0 && n < f.MinLength) || (f.MaxLength > 0 && n > f.MaxLength) {
			return fmt.Errorf("%s must be %s", name, f.LimitsHint())
		}
	}
	if f.Pattern != "" {
		re, err := regexp.Compile(`^(?:` + f.Pattern + `)$`)
		if err != nil {
			// A plugin bug, not the admin's: name it rather than accept.
			return fmt.Errorf("%s declares an invalid pattern: %v", name, err)
		}
		if !re.MatchString(value) {
			return fmt.Errorf("%s must match %s", name, f.Pattern)
		}
	}
	return nil
}

// LimitsHint renders the declared numeric or length limits as words
// ("8 to 20", "at most 20 characters"), or "" when none are declared.
func (f ConfigField) LimitsHint() string {
	if f.Min != nil || f.Max != nil {
		switch {
		case f.Min != nil && f.Max != nil:
			return fmt.Sprintf("%d to %d", *f.Min, *f.Max)
		case f.Min != nil:
			return fmt.Sprintf("at least %d", *f.Min)
		default:
			return fmt.Sprintf("at most %d", *f.Max)
		}
	}
	switch {
	case f.MinLength > 0 && f.MaxLength > 0:
		return fmt.Sprintf("%d to %d characters", f.MinLength, f.MaxLength)
	case f.MinLength > 0:
		return fmt.Sprintf("at least %d characters", f.MinLength)
	case f.MaxLength > 0:
		return fmt.Sprintf("at most %d characters", f.MaxLength)
	}
	return ""
}
