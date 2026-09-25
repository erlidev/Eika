// Package servertest describes the API's wire contract as shapes, so tests on
// both sides of it can check a body against what the Go types encode.
//
// A Shape is the JSON form of a Go type as encoding/json writes it: which
// fields an object has, which it may leave out, and which may be null. Of
// derives one from a type; Check reports how a decoded JSON value differs from
// one. The server's tests write the shapes of every route and event into
// docs/api/contract.json, which the frontend's mock harness enforces on
// every request it answers, so the mock cannot drift from the harness.
package servertest
