package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

// Progress is one progress report of a running request.
type Progress struct {
	Progress float64
	Total    *float64
	Message  string
}

// CallOptions are what a tool call adds to its arguments.
type CallOptions struct {
	// Headers are the Mcp-Param-* values of the call, from headerParams.
	Headers map[string]string
	// Progress receives the server's progress reports, if it sends any.
	Progress func(Progress)
	// Elicit asks the user when the server needs input to finish the call.
	Elicit ElicitFunc
}

// ListTools lists every tool the server offers.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	return listAll[Tool](ctx, c, "tools/list", "tools")
}

// ListResources lists every resource the server offers.
func (c *Client) ListResources(ctx context.Context) ([]Resource, error) {
	return listAll[Resource](ctx, c, "resources/list", "resources")
}

// ListResourceTemplates lists the server's resource templates.
func (c *Client) ListResourceTemplates(ctx context.Context) ([]ResourceTemplate, error) {
	return listAll[ResourceTemplate](ctx, c, "resources/templates/list", "resourceTemplates")
}

// ListPrompts lists every prompt the server offers.
func (c *Client) ListPrompts(ctx context.Context) ([]Prompt, error) {
	return listAll[Prompt](ctx, c, "prompts/list", "prompts")
}

// listAll follows a list's pages to the end.
func listAll[T any](ctx context.Context, c *Client, method, field string) ([]T, error) {
	var (
		out    []T
		cursor string
	)
	for range maxPages {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.request(ctx, method, params, callOpts{})
		if err != nil {
			return nil, err
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, fmt.Errorf("decode %s result: %w", method, err)
		}
		var page []T
		if items, ok := body[field]; ok && !bytes.Equal(items, []byte("null")) {
			if err := json.Unmarshal(items, &page); err != nil {
				return nil, fmt.Errorf("decode %s result: %w", method, err)
			}
		}
		out = append(out, page...)
		var next string
		_ = json.Unmarshal(body["nextCursor"], &next)
		if next == "" || next == cursor {
			return out, nil
		}
		cursor = next
	}
	return nil, fmt.Errorf("%s: the server kept paging past %d pages", method, maxPages)
}

// CallTool calls one tool with the model's arguments.
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage, o CallOptions) (*CallToolResult, error) {
	arguments := json.RawMessage("{}")
	if trimmed := bytes.TrimSpace(args); len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
		arguments = trimmed
	}
	opts := callOpts{name: name, headers: o.Headers, elicit: o.Elicit}
	if o.Progress != nil {
		opts.progress = func(p progressParams) {
			o.Progress(Progress{Progress: p.Progress, Total: p.Total, Message: p.Message})
		}
	}
	raw, err := c.request(ctx, "tools/call", map[string]any{"name": name, "arguments": arguments}, opts)
	if err != nil {
		return nil, err
	}
	var res CallToolResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("decode tools/call result: %w", err)
	}
	return &res, nil
}

// ReadResource reads one resource.
func (c *Client) ReadResource(ctx context.Context, uri string, elicit ElicitFunc) (*ReadResourceResult, error) {
	raw, err := c.request(ctx, "resources/read", map[string]any{"uri": uri}, callOpts{name: uri, elicit: elicit})
	if err != nil {
		return nil, err
	}
	var res ReadResourceResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("decode resources/read result: %w", err)
	}
	return &res, nil
}

// GetPrompt renders one prompt with its arguments.
func (c *Client) GetPrompt(ctx context.Context, name string, args map[string]string, elicit ElicitFunc) (*GetPromptResult, error) {
	params := map[string]any{"name": name}
	if len(args) > 0 {
		params["arguments"] = args
	}
	raw, err := c.request(ctx, "prompts/get", params, callOpts{name: name, elicit: elicit})
	if err != nil {
		return nil, err
	}
	var res GetPromptResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("decode prompts/get result: %w", err)
	}
	return &res, nil
}

// listens reports whether the connection gets list changes by opening a
// subscription, as a modern server sends them, rather than unasked.
func (c *Client) listens() bool {
	info := c.Info()
	return info.Era == EraModern && subscriptionFor(info.Capabilities) != (subscriptionFilter{})
}

// subscriptionFor is what a subscription asks of a server with these
// capabilities: every list change it can report.
func subscriptionFor(caps ServerCapabilities) subscriptionFilter {
	var f subscriptionFilter
	f.ToolsListChanged = caps.Tools != nil && caps.Tools.ListChanged
	f.PromptsListChanged = caps.Prompts != nil && caps.Prompts.ListChanged
	f.ResourcesListChanged = caps.Resources != nil && caps.Resources.ListChanged
	return f
}

// Listen holds a modern server's subscription open, delivering its list
// changes to the client's Notify handler, until ctx ends or the server ends
// the subscription.
func (c *Client) Listen(ctx context.Context) error {
	filter := subscriptionFor(c.Info().Capabilities)
	_, err := c.roundTrip(ctx, "subscriptions/listen", map[string]any{"notifications": filter}, callOpts{})
	return err
}
