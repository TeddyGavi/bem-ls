package bem

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Severity is how a rule's findings are reported. The values match the LSP
// DiagnosticSeverity numbers, with SeverityOff added as 0.
type Severity int

const (
	SeverityOff     Severity = 0
	SeverityError   Severity = 1
	SeverityWarning Severity = 2
	SeverityInfo    Severity = 3
	SeverityHint    Severity = 4
)

var severityNames = map[Severity]string{
	SeverityOff:     "off",
	SeverityError:   "error",
	SeverityWarning: "warning",
	SeverityInfo:    "info",
	SeverityHint:    "hint",
}

func (s Severity) String() string {
	if n, ok := severityNames[s]; ok {
		return n
	}
	return fmt.Sprintf("Severity(%d)", int(s))
}

// ParseSeverity parses "off", "hint", "info", "warning" or "error".
func ParseSeverity(s string) (Severity, error) {
	switch strings.ToLower(s) {
	case "information":
		return SeverityInfo, nil
	case "warn":
		return SeverityWarning, nil
	}
	for sev, name := range severityNames {
		if strings.EqualFold(s, name) {
			return sev, nil
		}
	}
	return 0, fmt.Errorf("unknown severity %q (want off, hint, info, warning or error)", s)
}

// MarshalText implements encoding.TextMarshaler.
func (s Severity) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler.
func (s *Severity) UnmarshalText(b []byte) error {
	v, err := ParseSeverity(string(b))
	if err == nil {
		*s = v
	}
	return err
}

// Rule identifies a check. The values are the names used in configuration.
type Rule string

const (
	// RuleInvalidName: a class doesn't follow the naming convention.
	RuleInvalidName Rule = "invalidName"
	// RuleNestedElement: an element of an element, like b__e1__e2.
	RuleNestedElement Rule = "nestedElement"
	// RuleModifierWithoutBase: b--m on an element that doesn't also have b.
	RuleModifierWithoutBase Rule = "modifierWithoutBase"
	// RuleElementOutsideBlock: b__e with no ancestor using block b.
	RuleElementOutsideBlock Rule = "elementOutsideBlock"
	// RuleMultipleBlocksMix: one element carries two different blocks.
	RuleMultipleBlocksMix Rule = "multipleBlocksMix"
	// RuleMissingInStylesheet: a class has no selector in the stylesheet.
	RuleMissingInStylesheet Rule = "missingInStylesheet"
	// RuleUnusedSelector: a stylesheet class no component uses.
	RuleUnusedSelector Rule = "unusedSelector"
	// RuleModuleClassMissing: styles.x where the CSS module has no .x.
	RuleModuleClassMissing Rule = "moduleClassMissing"
)

// Rules lists every rule.
var Rules = []Rule{
	RuleInvalidName, RuleNestedElement, RuleModifierWithoutBase, RuleElementOutsideBlock,
	RuleMultipleBlocksMix, RuleMissingInStylesheet, RuleUnusedSelector, RuleModuleClassMissing,
}

// DefaultSeverities are used for rules the configuration doesn't mention.
var DefaultSeverities = map[Rule]Severity{
	RuleInvalidName:         SeverityWarning,
	RuleNestedElement:       SeverityInfo,
	RuleModifierWithoutBase: SeverityWarning,
	RuleElementOutsideBlock: SeverityWarning,
	RuleMultipleBlocksMix:   SeverityInfo,
	RuleMissingInStylesheet: SeverityWarning,
	RuleUnusedSelector:      SeverityHint,
	RuleModuleClassMissing:  SeverityError,
}

// Config configures a Linter. The zero value means the classic preset with
// default severities.
type Config struct {
	Naming NamingConfig      `json:"naming"`
	Rules  map[Rule]Severity `json:"rules,omitempty"`
	// Ignore holds regular expressions. Classes matching any of them are
	// skipped by every rule, e.g. "^js-" or "^(container|row)$".
	Ignore []string `json:"ignore,omitempty"`
}

// Validate reports unknown rule names, which are otherwise silently
// ignored.
func (c Config) Validate() error {
	var unknown []string
	for r := range c.Rules {
		if _, ok := DefaultSeverities[r]; !ok {
			unknown = append(unknown, string(r))
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("unknown rule(s): %s", strings.Join(unknown, ", "))
	}
	return nil
}

func compileIgnore(pats []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(pats))
	for _, p := range pats {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("bad ignore pattern %q: %v", p, err)
		}
		out = append(out, re)
	}
	return out, nil
}
