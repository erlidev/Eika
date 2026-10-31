// Package probetest serves the fixtures of internal/provider/probe as fake
// servers: one constructor per backend, each answering the routes the probe
// reads with the recorded (llama.cpp) or documentation-derived (the others)
// responses in testdata. testdata/README.md says which is which.
//
// The entry points are LlamaCpp, VLLM, SGLang, Tabby and Cloud.
package probetest
