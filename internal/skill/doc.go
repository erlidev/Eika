// Package skill reads Agent Skills (agentskills.io): a lenient SKILL.md parser
// and validator for the six specification fields, and a bounded reader for a
// skill's .zip. Its Installer is the single writer of installed skills; it
// reaches the skills volume and the store through the Volume and Index
// interfaces and imports nothing of the module.
package skill
