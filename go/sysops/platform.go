package sysops

import "runtime"

// PlatformInfo identifies the running host's operating system and CPU
// architecture, in Go's own naming (runtime.GOOS / runtime.GOARCH values),
// so a caller can gate a capability without hand-rolling uname parsing.
type PlatformInfo struct {
	OS   string
	Arch string
}

// Platform reports the current process's OS and architecture.
func Platform() PlatformInfo {
	return PlatformInfo{OS: runtime.GOOS, Arch: runtime.GOARCH}
}

// String renders the platform as "os/arch", matching Go's own
// GOOS/GOARCH pairing convention.
func (p PlatformInfo) String() string {
	return p.OS + "/" + p.Arch
}
