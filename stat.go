package fsp

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// fileStat file stat info
type fileStat struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
}

// Size length in bytes for regular file
func (s fileStat) Size() int64 {
	return s.size
}

// Name base name of the file
func (s fileStat) Name() string {
	return filepath.Base(s.name)
}

// Mode file mode bits
func (s fileStat) Mode() os.FileMode {
	return s.mode
}

// ModTime modification time
func (s fileStat) ModTime() time.Time {
	return s.modTime
}

// IsDir abbreviation for Mode().IsDir()
func (s fileStat) IsDir() bool {
	return s.mode.IsDir()
}

// Sys underlying data source (can return nil)
func (s fileStat) Sys() interface{} {
	return nil
}

// fspError is the error type usually returned by functions in the fsp package
type fspError struct {
	Cmd    uint8  // FSP command operator
	Reason string // FSP command fail reason
	Err    error
}

func newOpError(errStr string) (err *fspError) {
	err = &fspError{
		Err: errors.New(errStr),
	}
	return
}

// Timeout check is time out error
func (e *fspError) Timeout() bool {
	if e == nil {
		return false
	}
	if strings.Contains(e.Reason, "time out") {
		return true
	}
	if e.Err != nil && strings.Contains(e.Err.Error(), "timeout") {
		return true
	}
	return false
}

func (e *fspError) Error() string {
	var s string
	if e == nil {
		return "<nil>"
	}
	if e.Reason != "" {
		s = e.Reason
	} else {
		s = e.Err.Error()
	}
	return s
}
