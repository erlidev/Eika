// Package builtin holds bash, file_read, file_write, file_edit, ask_user,
// web_search, web_fetch, history_search and history_read. Registry registers the stateless tools; each call
// receives its executor through tool.CallContext.
//
// File tools read bounded UTF-8 text and replace files or one exact match.
// Command output keeps its start and end when it exceeds the byte limit.
// Limits are in limits.go. File and process actions use the executor only.
// Web tools use internal/search; a web_fetch filter runs in the computer.
package builtin
