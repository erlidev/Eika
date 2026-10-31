// Package live runs scripted scenarios against the real model on the live
// test bench (docs/PLAN.md, "Live test bench") and writes what they measured
// to a JSON report.
//
// It reads the bench's endpoint and key from ~/.config/eika/live.env without
// overriding the environment, connects to the endpoint, records the server's
// build and model revision, and sends each scenario's requests through
// internal/provider/openai, so a scenario exercises the provider the harness
// uses. The live tests in this directory run behind the live build tag by
// `make live`; everything else here is plain code with ordinary tests, which
// `make check` runs. evals/README.md says how to run the suite and read a
// report.
package live
