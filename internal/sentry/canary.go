package sentry

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
)

// canaryPrefix opens an OpenAI project key, which is what the canary imitates.
const canaryPrefix = "sk-proj-"

// canaryBody is the number of random characters after the prefix, about the
// length of a real key.
const canaryBody = 156

// canaryReason is the halt note and audit reason of a canary hit. It names no
// value.
const canaryReason = "a canary key was found in a tool call"

// CanaryName is the environment variable the canary is planted under, the one
// the OpenAI libraries read.
const CanaryName = "OPENAI_API_KEY"

// NewCanary returns a fake OpenAI-shaped key. It is letters and digits only,
// so it reads the same in JSON arguments as in a shell.
func NewCanary() (string, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	out := make([]byte, 0, len(canaryPrefix)+canaryBody)
	out = append(out, canaryPrefix...)
	buf := make([]byte, 64)
	for len(out) < cap(out) {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("sentry: make canary: %w", err)
		}
		for _, b := range buf {
			// 248 is the largest multiple of 62 in a byte: no modulo bias.
			if b < 248 && len(out) < cap(out) {
				out = append(out, alphabet[b%62])
			}
		}
	}
	return string(out), nil
}

// canaryIn reports whether the canary's literal value is in the call's
// arguments, whatever the tool. It is one substring search, so it costs next
// to nothing at 64 KiB.
func (g *gate) canaryIn(a Action) bool {
	if g.Canary == nil {
		return false
	}
	c := g.Canary()
	return c != "" && bytes.Contains(a.Args, []byte(c))
}

// haltForCanary halts Eika. The caller's context may be cancelled by the very
// abort the halt causes, so the halt does not depend on it.
func (g *gate) haltForCanary(ctx context.Context) error {
	ctx = context.WithoutCancel(ctx)
	if g.Halter != nil {
		return g.Halter(ctx, canaryReason)
	}
	_, _, err := g.Halt(ctx, canaryReason)
	return err
}
