package sysops

import "fmt"

// RlimitInfo is one resource's soft/hard ulimit, in the unit that resource
// is measured in (bytes for RLIMIT_AS, file descriptors for RLIMIT_NOFILE).
type RlimitInfo struct {
	Soft uint64
	Hard uint64
}

// Requirement states the minimum a Guard preflight must find before a caller
// proceeds with a memory- or descriptor-heavy operation.
type Requirement struct {
	// MinFreeMemoryBytes is the free-memory floor. Zero skips the check.
	MinFreeMemoryBytes uint64
	// MinOpenFiles is the open-file-descriptor soft-limit floor. Zero skips
	// the check.
	MinOpenFiles uint64
}

// Guard preflights host resources before a caller commits to work that
// assumes they're available, so a heavy operation fails fast with a clear
// reason instead of partway through with an OS-level OOM or "too many open
// files".
type Guard struct{}

// NewGuard returns a Guard. It holds no state; every call reads the host
// fresh.
func NewGuard() *Guard {
	return &Guard{}
}

// Preflight checks req against the current host and returns an error naming
// the first requirement not met. A zero Requirement field is a no-op check.
func (g *Guard) Preflight(req Requirement) error {
	if req.MinFreeMemoryBytes > 0 {
		free, err := freeMemoryBytes()
		if err != nil {
			return fmt.Errorf("sysops: preflight free memory: %w", err)
		}
		if free < req.MinFreeMemoryBytes {
			return fmt.Errorf("sysops: preflight: free memory %d bytes below required %d", free, req.MinFreeMemoryBytes)
		}
	}
	if req.MinOpenFiles > 0 {
		lim, err := openFileLimit()
		if err != nil {
			return fmt.Errorf("sysops: preflight open-file limit: %w", err)
		}
		if lim.Soft < req.MinOpenFiles {
			return fmt.Errorf("sysops: preflight: open-file soft limit %d below required %d", lim.Soft, req.MinOpenFiles)
		}
	}
	return nil
}

// FreeMemoryBytes reports the host's currently available memory.
func (g *Guard) FreeMemoryBytes() (uint64, error) {
	return freeMemoryBytes()
}

// OpenFileLimit reports the calling process's RLIMIT_NOFILE soft/hard
// values.
func (g *Guard) OpenFileLimit() (RlimitInfo, error) {
	return openFileLimit()
}
