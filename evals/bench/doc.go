// Package bench runs the live test bench's server operations: the changes to
// the RTX 5090 server that some live suites need (docs/PLAN.md, "Live test
// bench"). A suite asks a Runner to serve the chat model as it is, with other
// flags, or the second backend, reads the server's logs and GPU, and the
// Runner puts the server back as it found it.
//
// The Runner reaches the host through a Transport, one persistent SSH
// ControlMaster per run, so every command is an argument list that a test
// replaces with a fake host. It records what runs on the GPU before it
// changes anything and refuses to start when anything but the chat model holds
// it, unless the caller named that container in Options.Take. It stops nothing
// else, edits no file on the server, and runs a changed model as a container
// of its own, so the Deployment Komodo manages is never edited. A run is put
// back by Close, on an interrupt, and by a watchdog on the server when the
// process or the connection is lost; evals/README.md describes the whole
// protocol.
//
// The package depends on the standard library only.
package bench
