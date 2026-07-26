package logkit

import (
	"os"
	"path/filepath"
	"runtime"
)

// captureCaller walks the stack skip frames above its own caller and returns
// the log call's source location, or nil if the runtime cannot resolve one.
// The file is relativized to the process's working directory so it never
// carries a build machine's absolute layout.
func captureCaller(skip int) *Caller {
	pc, file, line, ok := runtime.Caller(skip)
	if !ok {
		return nil
	}
	c := &Caller{File: relativize(file), Line: line}
	if fn := runtime.FuncForPC(pc); fn != nil {
		c.Function = filepath.Base(fn.Name())
	}
	return c
}

func relativize(file string) string {
	wd, err := os.Getwd()
	if err != nil {
		return filepath.Base(file)
	}
	rel, err := filepath.Rel(wd, file)
	if err != nil {
		return filepath.Base(file)
	}
	return filepath.ToSlash(rel)
}
