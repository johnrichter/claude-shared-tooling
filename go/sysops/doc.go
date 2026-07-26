// Package sysops wraps the OS/hardware surface an agent tool touches most
// often and gets wrong: spawning a subprocess, checking the host has room to
// do the work, and knowing what host it's running on. Run bounds subprocess
// output capture so a runaway or hostile child can't exhaust memory; Guard
// preflights free memory and process ulimits before a caller commits to a
// heavy operation; Platform reports OS and architecture for capability
// gating.
package sysops
