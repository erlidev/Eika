// Package inference is the queue every model call in Core waits in. It
// arbitrates the GPU between sessions, agents and background work
// (docs/design/runtime.md section 3).
//
// A caller asks for a Lease with Queue.Acquire, names the pool it needs, how
// urgent the call is and how many tokens it expects to occupy, holds the lease
// for the one model call, and releases it. A lease never covers a tool call or
// a wait between two model calls. Counting requests (/tokenize,
// /apply-template) and probes that send no prompt take no lease.
//
// A pool admits as many leases as it has slots. A pool whose KV cache is
// shared also admits a call only while the running calls fit the cache with
// it. Waiting calls are served by priority (interactive, foreground,
// background, maintenance), each gaining a level for every aging period it
// waits, so none starves. Maintenance calls run only once the pool has been
// idle for a while. With the policy's PreemptRunning, an interactive call that
// cannot be admitted cancels running background or maintenance calls, which
// their callers retry.
//
// On an endpoint a pool lists in Hinted (a llama.cpp server with several
// slots), a call that continues a session (Need.Affinity) is placed on the slot
// of the server that holds the session's prefix, and Lease.SlotHint names it for
// the request. The queue keeps what each slot holds, per endpoint; a call whose
// slot is busy waits for it as long as reading the prefix again would take, and
// then takes a free one. Any other call names no slot.
//
// The package depends on the standard library alone: the callers translate
// provider rows into Endpoint values and call SetPools and SetPolicy when
// settings change. Snapshot reports what holds each pool and what waits
// behind it.
package inference
