// Package config loads Eika's configuration from a YAML file and environment
// variables and exposes it as a single Config value.
//
// Configuration is read once in main and passed down; no other package reads
// the environment. Built-in defaults are overridden by the YAML file, which is
// overridden by EIKA_* environment variables, so a deployment can keep a
// checked-in file and inject secrets at runtime.
//
// The entry points are Load and Config.String, which redacts secrets so that a
// Config can be logged safely. The package depends only on the standard
// library and a YAML decoder.
package config
