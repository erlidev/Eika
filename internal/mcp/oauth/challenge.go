package oauth

import "strings"

// Challenge is what a server's WWW-Authenticate Bearer challenge says: where
// its protected resource metadata is and which scopes it wants.
type Challenge struct {
	ResourceMetadata string `json:"resource_metadata,omitempty"`
	Scope            string `json:"scope,omitempty"`
	Error            string `json:"error,omitempty"`
	ErrorDescription string `json:"error_description,omitempty"`
}

// ParseChallenge reads the Bearer challenge from the values of a
// WWW-Authenticate header. A header may hold several challenges, of several
// schemes, in one value or many; the others are skipped.
func ParseChallenge(values []string) Challenge {
	var c Challenge
	for _, v := range values {
		params, ok := bearerParams(v)
		if !ok {
			continue
		}
		c.ResourceMetadata = params["resource_metadata"]
		c.Scope = params["scope"]
		c.Error = params["error"]
		c.ErrorDescription = params["error_description"]
		return c
	}
	return c
}

// bearerParams finds the Bearer challenge in one header value and returns
// its auth-params.
func bearerParams(value string) (map[string]string, bool) {
	s := &scanner{in: value}
	for !s.done() {
		scheme := s.token()
		if scheme == "" {
			s.skip()
			continue
		}
		params := s.params()
		if strings.EqualFold(scheme, "Bearer") {
			return params, true
		}
	}
	return nil, false
}

// scanner walks the challenge grammar of RFC 9110 section 11.
type scanner struct {
	in  string
	pos int
}

func (s *scanner) done() bool { return s.pos >= len(s.in) }

func (s *scanner) space() {
	for s.pos < len(s.in) && (s.in[s.pos] == ' ' || s.in[s.pos] == '\t') {
		s.pos++
	}
}

// skip steps over one character the grammar has no place for.
func (s *scanner) skip() { s.pos++ }

// token reads a token, or "" when none starts here.
func (s *scanner) token() string {
	s.space()
	start := s.pos
	for s.pos < len(s.in) && isTokenChar(s.in[s.pos]) {
		s.pos++
	}
	return s.in[start:s.pos]
}

// params reads the comma-separated auth-params that follow a scheme, and
// stops at the next scheme: a token that is not followed by "=".
func (s *scanner) params() map[string]string {
	out := map[string]string{}
	for {
		s.space()
		for s.pos < len(s.in) && s.in[s.pos] == ',' {
			s.pos++
			s.space()
		}
		if s.done() {
			return out
		}
		mark := s.pos
		name := s.token()
		s.space()
		if name == "" || s.done() || s.in[s.pos] != '=' {
			// Not a parameter: the next challenge's scheme, or something
			// the caller steps over.
			s.pos = mark
			return out
		}
		s.pos++ // '='
		s.space()
		out[strings.ToLower(name)] = s.value()
	}
}

// value reads a quoted string, or an unquoted value up to the next comma or
// space. RFC 9110 allows only a token unquoted, but servers send unquoted
// URLs, which are clear enough to read.
func (s *scanner) value() string {
	if s.done() || s.in[s.pos] != '"' {
		start := s.pos
		for s.pos < len(s.in) && s.in[s.pos] != ',' && s.in[s.pos] != ' ' && s.in[s.pos] != '\t' {
			s.pos++
		}
		return s.in[start:s.pos]
	}
	s.pos++
	var b strings.Builder
	for s.pos < len(s.in) {
		c := s.in[s.pos]
		s.pos++
		switch c {
		case '\\':
			if s.pos < len(s.in) {
				b.WriteByte(s.in[s.pos])
				s.pos++
			}
		case '"':
			return b.String()
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func isTokenChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0
}
