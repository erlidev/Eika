// Package config loads the deployment configuration of the Eika harness from
// built-in defaults, an optional YAML file, and environment variables, and
// exposes it as a single Config value.
//
// It holds only what the process needs before it can reach its database:
// addresses, paths, and the Docker topology. Everything a user chooses, the
// model providers and models, the default model, the sandbox image, the
// subagent limits, and the sign-in password, is set in the web UI and kept
// in the database instead. The defaults are the compose stack's, so a
// deployment usually sets nothing here.
//
// Configuration is read once in main and passed down; no other package reads
// the environment. The entry points are Load and Config.String, which redacts
// secrets so that a Config can be logged safely.
package config
