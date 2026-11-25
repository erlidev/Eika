package server

import (
	"net/http"
	"slices"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/agentdef"
	"github.com/erlidev/eika/internal/mcp"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/tool"
)

// toolBody is one tool on the wire: what the user turns on or off in a
// session.
type toolBody struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// NeedsComputer reports whether the tool can run only in a session with
	// a computer, which keeps it out of a chat.
	NeedsComputer bool `json:"needs_computer"`
	// Server names the MCP server whose tool this is; absent for a built-in
	// tool and for the resource tools, which reach every server of a run.
	Server string `json:"server,omitempty"`
	// Tokens is the estimated size of the tool's definition in a request,
	// which offering it costs every model call.
	Tokens int `json:"tokens"`
}

// toolsResponse is the body of GET /api/tools.
type toolsResponse struct {
	Tools []toolBody `json:"tools"`
}

// handleListTools lists every tool a run can offer the model, ordered by
// name, with whether it needs a computer and what it costs a request: the
// built-in tools, and those the MCP servers offered when they were last
// listed.
func (s *Server) handleListTools(w http.ResponseWriter, _ *http.Request) {
	out := toolsResponse{Tools: []toolBody{}}
	for _, t := range s.allTools().List() {
		out.Tools = append(out.Tools, toolBody{
			Name:          t.Name(),
			Description:   t.Description(),
			NeedsComputer: tool.NeedsComputer(t),
			Server:        mcp.ServerOf(t),
			Tokens:        agent.ToolTokens(provider.ToolDef{Name: t.Name(), Description: t.Description(), Schema: t.Schema()}),
		})
	}
	writeJSON(w, s.log, http.StatusOK, out)
}

// allTools is every tool a run can offer: the ones every run shares and
// those the MCP servers offered when they were last listed.
func (s *Server) allTools() *tool.Registry {
	return s.narrowed(func(t tool.Tool) bool { _, agentOnly := t.(tool.AgentOnly); return !agentOnly }, s.offeredMCP())
}

// runTools selects the agent definition's tools, limited by its snapshot.
func (s *Server) runTools(sess store.Session, pinned []string, _ []tool.Tool) *tool.Registry {
	d, _ := agentdef.Lookup(sess.Agent)
	return s.narrowed(func(t tool.Tool) bool {
		return slices.Contains(d.Tools, t.Name()) && (pinned == nil || slices.Contains(pinned, t.Name())) &&
			(sess.ComputerID != "" || !tool.NeedsComputer(t))
	}, nil)
}

// usesComputer reports whether the agent's tools need its computer.
func (s *Server) usesComputer(sess store.Session) bool {
	return sess.ComputerID != "" && slices.ContainsFunc(s.runTools(sess, nil, nil).List(), tool.NeedsComputer)
}

// narrowed is a registry of the tools every run shares and the MCP tools
// given that keep accepts.
func (s *Server) narrowed(keep func(tool.Tool) bool, mcpTools []tool.Tool) *tool.Registry {
	var out *tool.Registry
	if s.deps.Tools != nil {
		out = s.deps.Tools.Filter(keep)
	} else {
		out, _ = tool.NewRegistry()
	}
	for _, t := range mcpTools {
		if !keep(t) {
			continue
		}
		// A name a built-in tool or another server already has is left to
		// the first; the registry refuses a second.
		if err := out.Register(t); err != nil {
			s.log.Warn("leave out an mcp tool", "tool", t.Name(), "error", err)
		}
	}
	return out
}

// sessionToolNames lists the agent tools in name order.
func (s *Server) sessionToolNames(sess store.Session, choice []string) []string {
	names := []string{}
	for _, t := range s.runTools(sess, choice, s.offeredMCP()).List() {
		names = append(names, t.Name())
	}
	return names
}

// offeredMCP returns the tools the MCP servers offered when they were last
// listed, without connecting to any.
func (s *Server) offeredMCP() []tool.Tool {
	if s.deps.MCP == nil {
		return nil
	}
	return s.deps.MCP.Offered()
}
