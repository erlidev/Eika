package mcp

import "time"

// Every bound the MCP client applies lives here, so that one file answers
// "how long does the harness wait for a server, and how much does it take
// from one".
const (
	// maxMessageBytes bounds one JSON-RPC message from a server. A tool
	// result with an image is the largest thing a server sends.
	maxMessageBytes = 16 << 20
	// maxErrorBody bounds how much of a failed HTTP response is read, and
	// how much of it a message quotes.
	maxErrorBody   = 64 << 10
	maxErrorQuoted = 300

	// maxResultBytes is the most text of one tool result the model sees;
	// past it the middle is cut, as bash output is.
	maxResultBytes = 48 << 10
	// maxMediaBytes bounds the images and audio one result carries to the
	// UI. The model never sees them: a tool message is text.
	maxMediaBytes = 4 << 20

	// maxPages bounds how many pages one list request follows.
	maxPages = 50
	// maxInputRounds bounds the multi round-trip retries of one request, so
	// a server that keeps asking cannot hold a call forever.
	maxInputRounds = 8

	// requestTimeout bounds a request that is not a tool call: a list, a
	// read, a prompt, and each step of connecting.
	requestTimeout = 60 * time.Second
	// callTimeout bounds one tool call, whatever progress it reports.
	callTimeout = 10 * time.Minute
	// stdioProbeTimeout is how long a stdio server has to answer
	// server/discover before it is taken for an initialize-based one. A
	// server fetched by npx on its first start can take this long to boot.
	stdioProbeTimeout = 20 * time.Second
	// connectWait bounds how long a run waits for servers that are not
	// connected yet before it starts without their tools.
	connectWait = 15 * time.Second
	// retryAfter is how soon a server that failed to connect is tried again
	// when a run asks for it; before then its failure stands.
	retryAfter = 30 * time.Second
	// closeTimeout bounds the goodbye a closing connection sends: an HTTP
	// DELETE of a legacy session, or a stdio process asked to exit.
	closeTimeout = 5 * time.Second

	// maxLogLines is how many lines of a server's log the harness keeps for
	// the UI; maxLogLine bounds one of them.
	maxLogLines = 200
	maxLogLine  = 2000
)
