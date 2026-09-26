package server

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"github.com/erlidev/eika/internal/agent"
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
	// NeedsWorkspace reports whether the tool can run only in a session with
	// a workspace, which keeps it out of a chat.
	NeedsWorkspace bool `json:"needs_workspace"`
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

// setToolsRequest is the body of PUT /api/sessions/{id}/tools.
type setToolsRequest struct {
	// Tools names the tools the session's runs may offer the model, where
	// mcp__<server>__* is every tool of that server. It must be present; an
	// empty list is none, and null clears the session's choice, so that its
	// profile's applies. It is raw so that null and absent differ.
	Tools json.RawMessage `json:"tools"`
}

// handleListTools lists every tool a run can offer the model, ordered by
// name, with whether it needs a workspace and what it costs a request: the
// built-in tools, and those the MCP servers offered when they were last
// listed.
func (s *Server) handleListTools(w http.ResponseWriter, _ *http.Request) {
	out := toolsResponse{Tools: []toolBody{}}
	for _, t := range s.allTools().List() {
		out.Tools = append(out.Tools, toolBody{
			Name:           t.Name(),
			Description:    t.Description(),
			NeedsWorkspace: tool.NeedsWorkspace(t),
			Server:         mcp.ServerOf(t),
			Tokens:         agent.ToolTokens(provider.ToolDef{Name: t.Name(), Description: t.Description(), Schema: t.Schema()}),
		})
	}
	writeJSON(w, s.log, http.StatusOK, out)
}

// handleSetSessionTools chooses the tools a session's next run may offer the
// model. A run already going keeps the tools it started with.
func (s *Server) handleSetSessionTools(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[setToolsRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var choice *[]string
	if len(req.Tools) == 0 || json.Unmarshal(req.Tools, &choice) != nil {
		s.fail(w, r, invalidf("tools is required: a list of tool names, empty for none, or null for the profile's"))
		return
	}
	id := r.PathValue("id")
	sess, err := s.deps.Store.Session(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var names []string
	if choice != nil {
		if names, err = s.checkToolChoice(r.Context(), *choice, sess.Chat()); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if err := s.deps.Store.SetSessionTools(r.Context(), id, names); err != nil {
		s.fail(w, r, err)
		return
	}
	sess.Tools = names
	s.log.Info("session tools set", "session_id", id, "tools", names)
	s.writeSession(w, r, http.StatusOK, sess)
}

// checkToolChoice validates a tool choice and returns it sorted, without
// repeats. Every entry names a tool a run can offer, or is
// mcp__<server>__* for a configured MCP server. A choice for a chat names
// nothing that needs a workspace.
func (s *Server) checkToolChoice(ctx context.Context, choice []string, chat bool) ([]string, error) {
	var servers []store.MCPServer
	if slices.ContainsFunc(choice, isServerChoice) && s.deps.MCP != nil {
		var err error
		if servers, err = s.deps.Store.MCPServers(ctx); err != nil {
			return nil, err
		}
	}
	names := make([]string, 0, len(choice))
	for _, name := range choice {
		if server, ok := choiceServer(name); ok {
			i := slices.IndexFunc(servers, func(m store.MCPServer) bool { return m.Name == server })
			switch {
			case i < 0:
				return nil, invalidf("there is no MCP server %q for %s", server, name)
			case chat && servers[i].Kind == mcp.KindStdio:
				return nil, invalidf("the tools of %s run in a workspace, and a chat has none", server)
			}
		} else {
			t, ok := s.toolNamed(name)
			switch {
			case !ok:
				return nil, invalidf("there is no tool %q", name)
			case chat && tool.NeedsWorkspace(t):
				return nil, invalidf("%s needs a workspace, and a chat has none", name)
			}
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names, nil
}

// serverChoiceSuffix ends a tool choice entry that takes every tool of one
// MCP server: mcp__<server>__*.
const serverChoiceSuffix = "__*"

// isServerChoice reports whether a tool choice entry takes every tool of
// an MCP server.
func isServerChoice(name string) bool {
	_, ok := choiceServer(name)
	return ok
}

// choiceServer returns the server a tool choice entry of the form
// mcp__<server>__* names.
func choiceServer(name string) (string, bool) {
	server, ok := strings.CutPrefix(name, mcp.ToolPrefix)
	if !ok {
		return "", false
	}
	server, ok = strings.CutSuffix(server, serverChoiceSuffix)
	if !ok || server == "" {
		return "", false
	}
	return server, true
}

// toolChosen is how a tool choice is resolved against the tools a run can
// offer: nil takes every tool, and an entry takes the tool it names or, as
// mcp__<server>__*, every tool that server offers, including one it adds
// after the choice was made.
func toolChosen(choice []string, t tool.Tool) bool {
	if choice == nil {
		return true
	}
	name, server := t.Name(), mcp.ServerOf(t)
	return slices.ContainsFunc(choice, func(entry string) bool {
		if s, ok := choiceServer(entry); ok {
			return server != "" && s == server
		}
		return entry == name
	})
}

// toolNamed finds a tool in the registry every run shares or among those
// the MCP servers offer.
func (s *Server) toolNamed(name string) (tool.Tool, bool) {
	return s.allTools().Get(name)
}

// allTools is every tool a run can offer: the ones every run shares and
// those the MCP servers offered when they were last listed.
func (s *Server) allTools() *tool.Registry {
	return s.narrowed(func(tool.Tool) bool { return true }, s.offeredMCP())
}

// runTools is the registry a run of sess offers the model: the tools every
// run shares and the MCP tools given, narrowed to what the tool choice
// takes and the session can run. A chat's runs never hold a tool that needs
// a workspace. Narrowing the registry, rather than refusing a call, means
// the model is never told about a tool it may not use.
func (s *Server) runTools(sess store.Session, choice []string, mcpTools []tool.Tool) *tool.Registry {
	return s.narrowed(func(t tool.Tool) bool {
		if sess.Chat() && tool.NeedsWorkspace(t) {
			return false
		}
		return toolChosen(choice, t)
	}, mcpTools)
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

// sessionToolNames lists the tools a run of sess with the tool choice given
// offers the model, ordered by name, taking each MCP server's tools as it
// last listed them.
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

// mcpToolsFor connects the MCP servers a run of sess may use, remote ones
// and the stdio ones in its workspace, and returns their tools. A tool
// choice that names no MCP tool connects none, so a stdio server is not
// started for a run that cannot call it.
func (s *Server) mcpToolsFor(ctx context.Context, sess store.Session, choice []string) []tool.Tool {
	if s.deps.MCP == nil {
		return nil
	}
	if choice != nil && !slices.ContainsFunc(choice, isMCPTool) {
		return nil
	}
	return s.deps.MCP.Tools(ctx, sess.WorkspaceID)
}

// isMCPTool reports whether a tool name is one the MCP pool offers: a
// server's tool or a resource tool.
func isMCPTool(name string) bool { return strings.HasPrefix(name, "mcp_") }
