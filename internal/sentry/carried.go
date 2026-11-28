package sentry

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"hash/maphash"
	"net/url"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// MarkCarried is an access mark: an outbound call's arguments hold text the
// session read from a private source. Its value is empty.
const MarkCarried MarkKind = "carried"

// Fingerprint parameters (docs/design/sentry.md, section 11).
const (
	fingerprintWords  = 8      // words in one fingerprint
	winnowWindow      = 4      // a fingerprint is kept per window of this many
	maxFingerprints   = 50_000 // per session, the oldest dropped
	maxFingerprintSes = 64     // sessions kept; the least recently read is dropped
	maxPrivateBytes   = 4 << 20
	maxCheckedBytes   = 64 << 10 // an argument is checked up to this many bytes
	minCarriedChars   = 24       // a fingerprint's words span at least this much
	maxDecodeDepth    = 3
)

// privateStore holds each session's fingerprints of private text. Only hashes
// of eight-word runs under a seed that exists in memory alone are kept: the
// text, and any word of it, is never stored, logged or written to a mark.
type privateStore struct {
	mu       sync.Mutex
	seed     maphash.Seed
	sessions map[string]*fingerprints
	clock    uint64
}

// fingerprints is one session's set, with the order its members arrived in.
type fingerprints struct {
	set   map[uint64]struct{}
	order []uint64 // oldest first
	used  uint64
}

// words splits text into lower-case runs of letters and digits, with the
// character count of each, so punctuation, line breaks and escapes between words
// do not matter.
func words(text string) (ws []string, chars []int) {
	if lowerable(text) {
		text = strings.ToLower(text)
	}
	start, n := -1, 0
	flush := func(end int) {
		if start >= 0 {
			ws = append(ws, text[start:end])
			chars = append(chars, n)
			start, n = -1, 0
		}
	}
	for i := 0; i < len(text); {
		c := text[i]
		if c < utf8.RuneSelf {
			if asciiWord[c] {
				if start < 0 {
					start = i
				}
				n++
			} else if start >= 0 {
				flush(i)
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			if start < 0 {
				start = i
			}
			n++
		} else {
			flush(i)
		}
		i += size
	}
	flush(len(text))
	return ws, chars
}

// asciiWord marks the lower-case letters and digits.
var asciiWord = func() (t [utf8.RuneSelf]bool) {
	for c := range t {
		t[c] = c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
	}
	return t
}()

// lowerable reports whether strings.ToLower could change text: it has an
// upper-case letter or a byte outside ASCII. Text that is already lower-case
// ASCII is used as it is, without ToLower's copy.
func lowerable(text string) bool {
	for i := 0; i < len(text); i++ {
		if c := text[i]; c >= utf8.RuneSelf || c >= 'A' && c <= 'Z' {
			return true
		}
	}
	return false
}

// grams returns the hash of every run of fingerprintWords consecutive words,
// and the characters each run spans.
func (p *privateStore) grams(text string) (hashes []uint64, span []int) {
	ws, chars := words(text)
	if len(ws) < fingerprintWords {
		return nil, nil
	}
	var h maphash.Hash
	h.SetSeed(p.seed)
	for i := 0; i+fingerprintWords <= len(ws); i++ {
		h.Reset()
		n := 0
		for _, w := range ws[i : i+fingerprintWords] {
			h.WriteString(w)
			h.WriteByte(' ')
		}
		for _, c := range chars[i : i+fingerprintWords] {
			n += c
		}
		hashes = append(hashes, h.Sum64())
		span = append(span, n)
	}
	return hashes, span
}

// winnow keeps the smallest hash of each window (the rightmost on a tie), once.
// Any run of text at least winnowWindow+fingerprintWords-1 words long shares a
// kept hash with the original.
func winnow(hashes []uint64) []uint64 {
	var out []uint64
	last := -1
	for i := 0; i+winnowWindow <= len(hashes); i++ {
		best := i
		for j := i + 1; j < i+winnowWindow; j++ {
			if hashes[j] <= hashes[best] {
				best = j
			}
		}
		if best != last {
			out = append(out, hashes[best])
			last = best
		}
	}
	if len(hashes) > 0 && len(hashes) < winnowWindow {
		out = append(out, slices.Min(hashes))
	}
	return out
}

// add keeps the fingerprints of text for a session.
func (p *privateStore) add(session, text string) {
	if len(text) > maxPrivateBytes {
		text = text[:maxPrivateBytes]
	}
	hashes, _ := p.grams(text)
	kept := winnow(hashes)
	if len(kept) == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sessions == nil {
		p.sessions = map[string]*fingerprints{}
	}
	fp := p.sessions[session]
	if fp == nil {
		if len(p.sessions) >= maxFingerprintSes {
			var oldest string
			for id, s := range p.sessions {
				if oldest == "" || s.used < p.sessions[oldest].used {
					oldest = id
				}
			}
			delete(p.sessions, oldest)
		}
		fp = &fingerprints{set: map[uint64]struct{}{}}
		p.sessions[session] = fp
	}
	p.clock++
	fp.used = p.clock
	for _, h := range kept {
		if _, ok := fp.set[h]; ok {
			continue
		}
		fp.set[h] = struct{}{}
		fp.order = append(fp.order, h)
	}
	if over := len(fp.order) - maxFingerprints; over > 0 {
		for _, h := range fp.order[:over] {
			delete(fp.set, h)
		}
		fp.order = append([]uint64(nil), fp.order[over:]...)
	}
}

// carries reports whether any of the texts holds a run of a session's private
// text: eight consecutive words that span at least minCarriedChars characters.
func (p *privateStore) carries(session string, texts []string) bool {
	p.mu.Lock()
	fp := p.sessions[session]
	empty := fp == nil || len(fp.set) == 0
	p.mu.Unlock()
	if empty {
		return false
	}
	for _, text := range texts {
		for _, form := range decodedForms(text) {
			hashes, span := p.grams(form)
			p.mu.Lock()
			for i, h := range hashes {
				if _, ok := fp.set[h]; ok && span[i] >= minCarriedChars {
					p.mu.Unlock()
					return true
				}
			}
			p.mu.Unlock()
		}
	}
	return false
}

// byteClass is a set of bytes.
type byteClass [256]bool

func classOf(ok func(byte) bool) *byteClass {
	var c byteClass
	for i := range c {
		c[i] = ok(byte(i))
	}
	return &c
}

var (
	base64Bytes = classOf(func(c byte) bool {
		return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '+' || c == '/' || c == '_' || c == '-' || c == '='
	})
	hexBytes = classOf(func(c byte) bool { return unhex(c) >= 0 })
)

// runsOf returns up to limit runs of at least minLen bytes that are all in
// class.
func runsOf(text string, class *byteClass, minLen, limit int) []string {
	var out []string
	start := -1
	for i := 0; i <= len(text) && len(out) < limit; i++ {
		if i < len(text) && class[text[i]] {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 && i-start >= minLen {
			out = append(out, text[start:i])
		}
		start = -1
	}
	return out
}

// decodedForms returns the text and what hides in it: the text after each layer
// of percent-decoding, and the decoded text of each run of base64 and hex in
// any of them, up to maxDecodeDepth layers. It looks at the first
// maxCheckedBytes of the text.
func decodedForms(text string) []string {
	if len(text) > maxCheckedBytes {
		text = text[:maxCheckedBytes]
	}
	out := []string{text}
	queue := []string{text}
	for depth := 0; depth < maxDecodeDepth && len(queue) > 0; depth++ {
		var next []string
		for _, t := range queue {
			if escaped(t) {
				if u, err := url.QueryUnescape(t); err == nil && u != t {
					next = append(next, u)
				} else if l := lenientUnescape(t); l != t {
					next = append(next, l)
				}
			}
			for _, run := range runsOf(t, base64Bytes, 32, 8) {
				next = append(next, decodeBase64(run)...)
			}
			for _, run := range runsOf(t, hexBytes, 48, 8) {
				if len(run)%2 == 1 {
					run = run[:len(run)-1]
				}
				if b, err := hex.DecodeString(run); err == nil && utf8.Valid(b) {
					next = append(next, string(b))
				}
			}
		}
		out = append(out, next...)
		queue = next
		if len(out) > 64 {
			break
		}
	}
	return out
}

// decodeBase64 decodes a run in either alphabet. The run may begin partway
// through a group, so each of the four alignments is tried. Only a run whose
// start decodes to text is decoded in full.
func decodeBase64(run string) []string {
	run = strings.TrimRight(run, "=")
	var out []string
	for off := range 4 {
		part := run[off:]
		part = part[:len(part)/4*4]
		for _, enc := range []*base64.Encoding{base64.RawStdEncoding, base64.RawURLEncoding} {
			if sample := part[:min(len(part), 128)]; !texty(enc, sample) {
				continue
			}
			if b, err := enc.DecodeString(part); err == nil {
				out = append(out, strings.ToValidUTF8(string(b), " "))
			}
		}
	}
	return out
}

// texty reports whether the start of an encoded run decodes to plain text.
func texty(enc *base64.Encoding, sample string) bool {
	b, err := enc.DecodeString(sample)
	if err != nil || len(b) == 0 {
		return false
	}
	bad := 0
	for _, r := range string(b) {
		if r == utf8.RuneError || (r < 0x20 && r != '\n' && r != '\t' && r != '\r') {
			bad++
		}
	}
	return bad*20 < utf8.RuneCount(b)
}

// notePrivate keeps the fingerprints of text a session read from a private
// source.
func (g *gate) notePrivate(session, text string) {
	if session == "" {
		return
	}
	g.private.add(session, text)
}

// markCarried records a carried mark when an allowed outbound call's arguments
// hold private text, and reports whether they do. Like every mark it changes
// no verdict; the tripwires read it.
func (g *gate) markCarried(ctx context.Context, a Action, args []string) bool {
	if g.Marks == nil || len(args) == 0 || !g.private.carries(a.Principal.Session, args) {
		return false
	}
	m := Mark{Session: a.Principal.Session, Run: a.Principal.Run, Computer: a.Principal.Computer,
		Kind: MarkCarried, SourceName: a.Name, Key: a.Key}
	if err := g.mark(ctx, m); err != nil {
		g.Log.Error("record carried", "error", err)
	}
	return true
}
