package memory

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const (
	// MaxBodyBytes is the largest memory body in bytes.
	MaxBodyBytes = 2048
	// MaxFileBytes bounds a file including its frontmatter.
	MaxFileBytes = 16384
)

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// File is a memory and its tracked frontmatter. Source is a citation.
type File struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Type        string `yaml:"type"`
	Source      string `yaml:"source"`
	Created     string `yaml:"created"`
	Updated     string `yaml:"updated"`
	Expires     string `yaml:"expires"`
	Pinned      bool   `yaml:"pinned"`
	State       string `yaml:"state"`
	Body        string `yaml:"-"`
}

// Validate checks the file name and the memory's format and bounds.
func (f File) Validate(filename string) error {
	if !slugPattern.MatchString(f.Name) || filename != f.Name+".md" {
		return fmt.Errorf("invalid memory name or file name")
	}
	if !utf8.ValidString(f.Description) || strings.ContainsRune(f.Description, 0) || utf8.RuneCountInString(f.Description) < 20 || utf8.RuneCountInString(f.Description) > 200 || strings.ContainsAny(f.Description, "\r\n") {
		return fmt.Errorf("invalid description")
	}
	switch f.Type {
	case "user", "preference", "person", "fact", "procedure":
	default:
		return fmt.Errorf("invalid type")
	}
	switch f.State {
	case "active", "pending", "archived":
	default:
		return fmt.Errorf("invalid state")
	}
	if strings.TrimSpace(f.Source) == "" || !utf8.ValidString(f.Source) || strings.ContainsAny(f.Source, "\x00\r\n") {
		return fmt.Errorf("invalid source")
	}
	for _, value := range []string{f.Created, f.Updated} {
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			return fmt.Errorf("invalid timestamp: %w", err)
		}
	}
	if f.Expires != "" {
		if _, err := time.Parse("2006-01-02", f.Expires); err != nil {
			return fmt.Errorf("invalid expiry: %w", err)
		}
	}
	if len(f.Body) > MaxBodyBytes || !utf8.ValidString(f.Body) || strings.ContainsRune(f.Body, 0) {
		return fmt.Errorf("invalid body")
	}
	return nil
}

// Parse reads a bounded memory file and checks its name and frontmatter.
func Parse(filename string, data []byte) (File, error) {
	var f File
	if len(data) > MaxFileBytes || !utf8.Valid(data) {
		return f, fmt.Errorf("invalid file size or encoding")
	}
	// Delimiters occupy whole lines; preserve the body after the closing line.
	line, rest, ok := bytes.Cut(data, []byte("\n"))
	if !ok || string(bytes.TrimSuffix(line, []byte("\r"))) != "---" {
		return f, fmt.Errorf("missing frontmatter")
	}
	var header []byte
	for len(rest) > 0 {
		var next []byte
		line, next, ok = bytes.Cut(rest, []byte("\n"))
		if string(bytes.TrimSuffix(line, []byte("\r"))) == "---" {
			if !ok {
				next = nil
			}
			dec := yaml.NewDecoder(bytes.NewReader(header))
			dec.KnownFields(true)
			if err := dec.Decode(&f); err != nil {
				return File{}, fmt.Errorf("parse frontmatter: %w", err)
			}
			var extra any
			if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
				return File{}, fmt.Errorf("invalid frontmatter document")
			}
			f.Body = string(next)
			if err := f.Validate(filename); err != nil {
				return File{}, err
			}
			return f, nil
		}
		if !ok {
			break
		}
		header = append(header, line...)
		header = append(header, '\n')
		rest = next
	}
	return f, fmt.Errorf("missing closing frontmatter")
}

// Render returns a stable memory file after validating it.
func (f File) Render(filename string) ([]byte, error) {
	if err := f.Validate(filename); err != nil {
		return nil, err
	}
	header, err := yaml.Marshal(f)
	if err != nil {
		return nil, fmt.Errorf("render frontmatter: %w", err)
	}
	data := append([]byte("---\n"), header...)
	data = append(data, []byte("---\n")...)
	data = append(data, f.Body...)
	if len(data) > MaxFileBytes {
		return nil, fmt.Errorf("file exceeds size limit")
	}
	return data, nil
}
