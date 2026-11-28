package sentry

import (
	"context"
	"fmt"
	"slices"
)

// DestSource is where a host first appeared in a session
// (docs/design/sentry.md, section 3, "The first-seen index").
type DestSource string

// The sources of a destination. A host with no record has the source model:
// the model wrote it, or it came from a file or `bash` output.
const (
	// DestUser is a host in a message the user typed.
	DestUser DestSource = "user"
	// DestSearch is a host in a web_search result.
	DestSearch DestSource = "search"
	// DestContent is a host in other ingested content: a page, a report, a
	// child's transcript.
	DestContent DestSource = "content"
	// DestModel is every other host.
	DestModel DestSource = "model"
)

// DestinationLog stores the local destination index. Core supplies it
// (store.Store implements it). Hosts are canonical (CanonicalHost).
type DestinationLog interface {
	// RecordDestinations notes where hosts appeared in a session. The first
	// record of a host stays, except that a user record replaces any other: a
	// host the user typed is theirs whatever a page said before.
	RecordDestinations(ctx context.Context, session string, hosts []string, src DestSource, name string) error
	// DestinationSource returns a host's source in a session, and false for a
	// host with no record.
	DestinationSource(ctx context.Context, session, host string) (DestSource, bool, error)
	// InheritDestinations copies a session's records to a new child session.
	InheritDestinations(ctx context.Context, parent, child string) error
	// KnownHost reports whether a host is known: answered "this was fine", or
	// reached by this computer in an earlier run. An empty computer asks only
	// for the hosts known to every computer.
	KnownHost(ctx context.Context, computer, host string) (bool, error)
	// MarkKnown makes a host known to one computer, or to every computer when
	// computer is empty.
	MarkKnown(ctx context.Context, computer, host, reason string) error
}

// shippedHosts are the package registries and code hosts a computer reaches
// anyway, compared exactly: installing dependencies is not a sign of anything.
var shippedHosts = map[string]bool{
	"github.com": true, "api.github.com": true, "codeload.github.com": true,
	"raw.githubusercontent.com": true, "objects.githubusercontent.com": true,
	"gitlab.com": true, "bitbucket.org": true,
	"registry.npmjs.org": true, "registry.yarnpkg.com": true,
	"pypi.org": true, "files.pythonhosted.org": true,
	"proxy.golang.org": true, "sum.golang.org": true,
	"crates.io": true, "index.crates.io": true, "static.crates.io": true,
	"rubygems.org": true, "repo.maven.apache.org": true, "repo1.maven.org": true,
	"deb.debian.org": true, "security.debian.org": true,
}

// ObservationKind says what an Observation reports.
type ObservationKind string

// The observations so far.
const (
	// ObserveUserMessage is a message the user typed into Session.
	ObserveUserMessage ObservationKind = "user_message"
	// ObserveSubagentStart is a new subagent session, Session, whose parent
	// session is Parent.
	ObserveSubagentStart ObservationKind = "subagent_start"
	// ObserveEgress is bytes an allowed connection of Computer moved to Host
	// during Run: Sent and Received are since the last report, and Closed marks
	// the connection's last.
	ObserveEgress ObservationKind = "egress"
	// ObserveRunEnd is Run ending: the hosts its computer reached become known.
	ObserveRunEnd ObservationKind = "run_end"
	// ObservePrivateRead is private text Session read: a memory body or a file
	// read with a file tool, in Text. Only fingerprints of it are kept.
	ObservePrivateRead ObservationKind = "private_read"
)

// Observation is something that needs no decision. Each kind reads the fields
// its constant names.
type Observation struct {
	Kind    ObservationKind
	Session string
	Parent  string
	Text    string

	Run, Computer, Host string
	Sent, Received      int64
	Closed              bool
}

// Observe records an observation. A failure is logged and changes nothing
// else: a host that is not recorded is simply not anchored.
func (g *gate) Observe(ctx context.Context, o Observation) {
	switch o.Kind {
	case ObserveEgress:
		g.countEgress(o)
		g.markUpload(ctx, o)
		return
	case ObserveRunEnd:
		g.trip.End(o.Run)
		g.endRun(ctx, o.Run, g.endHold(o.Run))
		return
	case ObservePrivateRead:
		g.notePrivate(o.Session, o.Text)
		return
	}
	if o.Kind == ObserveSubagentStart {
		g.holdSubagent(o.Parent, o.Session)
	}
	if g.Destinations == nil || o.Session == "" {
		return
	}
	var err error
	switch o.Kind {
	case ObserveUserMessage:
		if hosts := hostsIn(o.Text); len(hosts) > 0 {
			err = g.Destinations.RecordDestinations(ctx, o.Session, hosts, DestUser, "user")
		}
	case ObserveSubagentStart:
		err = g.Destinations.InheritDestinations(ctx, o.Parent, o.Session)
	}
	if err != nil {
		g.Log.Error("record destinations", "kind", o.Kind, "session_id", o.Session, "error", err)
	}
}

// recordIngested notes the hosts in ingested text: the results of a web search
// as source search, everything else as content. The host of a search result is
// recorded with it; the host of a fetched page is not, because the page's
// address already came from the user, a search or the model.
func (g *gate) recordIngested(ctx context.Context, segs []Segment, texts []string) error {
	if g.Destinations == nil {
		return nil
	}
	for i, seg := range segs {
		src := seg.Source
		if src.Session == "" {
			continue
		}
		kind := DestContent
		hosts := hostsIn(texts[i])
		if src.Kind == SourceSearch {
			kind = DestSearch
			if host := SourceHost(src); host != "" && !slices.Contains(hosts, host) {
				hosts = append([]string{host}, hosts...)
			}
		}
		if len(hosts) == 0 {
			continue
		}
		if err := g.Destinations.RecordDestinations(ctx, src.Session, hosts, kind, sourceLabel(src)); err != nil {
			return fmt.Errorf("sentry: record destinations: %w", err)
		}
	}
	return nil
}

// anchored reports whether a destination is anchored for a session on a
// computer (docs/design/sentry.md, section 3). A host the user typed is. So is
// a known one. A URL, which is a fetch, is also anchored by a search result;
// an egress host is also anchored by the shipped list of registries and code
// hosts, and by the computer having reached it before. Page content, and
// anything the model wrote, anchors nothing.
func (g *gate) anchored(ctx context.Context, session, computer, host string, egress bool) (bool, error) {
	host = EgressHost(host)
	if host == "" {
		return false, nil
	}
	if egress && shippedHosts[host] {
		return true, nil
	}
	if g.Destinations == nil {
		return false, nil
	}
	src, ok, err := g.Destinations.DestinationSource(ctx, session, host)
	if err != nil {
		return false, fmt.Errorf("sentry: read destination source: %w", err)
	}
	if ok && (src == DestUser || (src == DestSearch && !egress)) {
		return true, nil
	}
	if !egress {
		computer = ""
	}
	known, err := g.Destinations.KnownHost(ctx, computer, host)
	if err != nil {
		return false, fmt.Errorf("sentry: read known host: %w", err)
	}
	return known, nil
}
