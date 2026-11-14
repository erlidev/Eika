package server

import "github.com/erlidev/eika/internal/agentdef"

// agentBody contains effective values, built-in defaults and stored overrides.
type agentBody struct {
	agentdef.Definition
	Builtin   agentdef.Definition `json:"builtin"`
	Overrides agentdef.Override   `json:"overrides"`
}

// agentsResponse lists all built-in agents in stable name order.
type agentsResponse struct {
	Agents []agentBody `json:"agents"`
}
