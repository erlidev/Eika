package skill

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const (
	// MaxSkillFileBytes bounds SKILL.md, frontmatter included.
	MaxSkillFileBytes = 64 << 10
	// LongBodyLines is the line count above which a body earns a warning.
	LongBodyLines = 500

	maxDescriptionChars = 1024
	maxCompatChars      = 500
)

var namePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Skill is a parsed SKILL.md. Body is the Markdown after the frontmatter.
// Warnings are problems the parser tolerated; a skill that cannot be used at
// all is an error from Parse instead.
type Skill struct {
	Name          string
	Description   string
	License       string
	Compatibility string
	Metadata      map[string]string
	AllowedTools  string
	Body          string
	Warnings      []string
}

// Parse reads a SKILL.md. dir is the directory name the file came from, or ""
// for a flat archive, where there is nothing to compare the name with.
//
// Parsing is lenient. A name that differs from dir, an unquoted colon in a
// value (retried quoted), an over-long description or compatibility, a bad
// metadata map, a missing name (dir is used) and unknown fields are warnings.
// The returned error is the reason to skip the skill: no frontmatter, YAML
// that cannot be parsed, no description, or no usable name (a name the
// installer could not safely use as a directory).
func Parse(dir string, data []byte) (Skill, error) {
	var s Skill
	if len(data) > MaxSkillFileBytes {
		return s, fmt.Errorf("SKILL.md is over %d bytes", MaxSkillFileBytes)
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return s, fmt.Errorf("SKILL.md is not UTF-8 text")
	}
	header, body, err := splitFrontmatter(data)
	if err != nil {
		return s, err
	}
	fields, err := decodeFields(header)
	if err != nil {
		// The common real-world fault: `description: Use when: x`. Quote each
		// such value and try once more.
		var err2 error
		if fields, err2 = decodeFields(quoteColonValues(header)); err2 != nil {
			return s, fmt.Errorf("frontmatter is not valid YAML: %w", err)
		}
		s.Warnings = append(s.Warnings, "frontmatter had an unquoted colon in a value; it was read as quoted")
	}
	s.Body = body

	s.Name = text(fields["name"])
	s.Description = strings.TrimSpace(text(fields["description"]))
	s.License = text(fields["license"])
	s.Compatibility = text(fields["compatibility"])
	s.AllowedTools = text(fields["allowed-tools"])

	if s.Description == "" {
		return Skill{}, fmt.Errorf("description is missing")
	}
	if n := utf8.RuneCountInString(s.Description); n > maxDescriptionChars {
		s.Warnings = append(s.Warnings, fmt.Sprintf("description is %d characters; the specification allows %d", n, maxDescriptionChars))
	}
	if n := utf8.RuneCountInString(s.Compatibility); n > maxCompatChars {
		s.Warnings = append(s.Warnings, fmt.Sprintf("compatibility is %d characters; the specification allows %d", n, maxCompatChars))
	}

	switch {
	case s.Name == "" && dir != "" && len(dir) <= 64 && namePattern.MatchString(dir):
		s.Name = dir
		s.Warnings = append(s.Warnings, "name is missing; the directory name was used")
	case s.Name == "":
		return Skill{}, fmt.Errorf("name is missing")
	case len(s.Name) > 64 || !namePattern.MatchString(s.Name):
		return Skill{}, fmt.Errorf("name must be 1 to 64 lowercase letters, digits and single hyphens, not starting or ending with a hyphen")
	case dir != "" && dir != s.Name:
		s.Warnings = append(s.Warnings, fmt.Sprintf("name %q does not match the directory %q", s.Name, dir))
	}

	if raw, ok := fields["metadata"]; ok && raw != nil {
		if m, ok := stringMap(raw); ok {
			s.Metadata = m
		} else {
			s.Warnings = append(s.Warnings, "metadata is not a map of strings to strings; it was ignored")
		}
	}
	var unknown []string
	for key := range fields {
		switch key {
		case "name", "description", "license", "compatibility", "metadata", "allowed-tools":
		default:
			unknown = append(unknown, key)
		}
	}
	sort.Strings(unknown)
	for _, key := range unknown {
		s.Warnings = append(s.Warnings, fmt.Sprintf("unknown frontmatter field %q was ignored", key))
	}

	if strings.Count(body, "\n")+1 > LongBodyLines {
		s.Warnings = append(s.Warnings, fmt.Sprintf("SKILL.md body is over %d lines", LongBodyLines))
	}
	return s, nil
}

// splitFrontmatter returns the YAML between the opening and closing "---"
// lines, and the body after the closing line.
func splitFrontmatter(data []byte) (header, body string, err error) {
	text := strings.TrimPrefix(string(data), "\ufeff")
	first, rest, ok := strings.Cut(text, "\n")
	if !ok || strings.TrimRight(first, "\r ") != "---" {
		return "", "", fmt.Errorf("SKILL.md has no frontmatter")
	}
	var head strings.Builder
	for rest != "" {
		line, next, _ := strings.Cut(rest, "\n")
		if strings.TrimRight(line, "\r ") == "---" {
			return head.String(), next, nil
		}
		head.WriteString(line)
		head.WriteByte('\n')
		rest = next
	}
	return "", "", fmt.Errorf("frontmatter is not closed")
}

func decodeFields(header string) (map[string]any, error) {
	var fields map[string]any
	if err := yaml.Unmarshal([]byte(header), &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		fields = map[string]any{}
	}
	return fields, nil
}

// plainValue matches a top-level `key: value` line whose value is an
// unquoted plain scalar that itself contains ": ".
var plainValue = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_-]*):[ \t]+([^"'\[{|>&*!#%@` + "`" + `\s][^\n]*: [^\n]*|[^"'\[{|>&*!#%@` + "`" + `\s][^\n]*:)$`)

func quoteColonValues(header string) string {
	lines := strings.Split(header, "\n")
	for i, line := range lines {
		line = strings.TrimRight(line, "\r")
		if m := plainValue.FindStringSubmatch(line); m != nil {
			lines[i] = m[1] + ": " + yamlQuote(m[2])
		}
	}
	return strings.Join(lines, "\n")
}

func yamlQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// text returns a scalar as a string; anything else is "".
func text(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case int, int64, float64, bool:
		return fmt.Sprint(v)
	}
	return ""
}

func stringMap(v any) (map[string]string, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	out := make(map[string]string, len(m))
	for k, raw := range m {
		str, ok := raw.(string)
		if !ok {
			// YAML reads an unquoted 1.0 as a number; the spec wants strings.
			if text(raw) == "" {
				return nil, false
			}
			str = text(raw)
		}
		out[k] = str
	}
	return out, true
}

// ValidName is whether name is a legal skill name: 1 to 64 lowercase letters
// and digits with single hyphens inside.
func ValidName(name string) bool {
	return len(name) >= 1 && len(name) <= 64 && namePattern.MatchString(name)
}
