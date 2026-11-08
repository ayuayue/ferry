//go:build linux

// Package procattr lists running programs for the rule editor.
//
// The Windows build reads a Toolhelp snapshot and pulls icons out of the
// executables with GDI. Linux exposes the same information under /proc, and has
// no equivalent for the icon: the picker falls back to its default glyph.
package procattr

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// Process is one executable with the number of times it is running.
type Process struct {
	PID       uint32 `json:"pid"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	Instances int    `json:"instances"`
	System    bool   `json:"system"`
}

// List returns the running programs, grouped by executable, A-Z.
//
// Only processes whose executable can be resolved are listed: as a normal user
// /proc/<pid>/exe is unreadable for other users' (and root's) processes, so the
// picker shows the signed-in user's programs. A rule can still be written by
// typing the image name by hand.
func List() []Process {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	byPath := make(map[string]*Process, 256)
	for _, entry := range entries {
		pid, err := strconv.ParseUint(entry.Name(), 10, 32)
		if err != nil || pid == uint64(os.Getpid()) {
			continue
		}
		exe, err := os.Readlink(filepath.Join("/proc", entry.Name(), "exe"))
		if err != nil || exe == "" {
			continue
		}
		exe = strings.TrimSuffix(exe, " (deleted)")
		if p, ok := byPath[exe]; ok {
			p.Instances++
			continue
		}
		byPath[exe] = &Process{
			PID:       uint32(pid),
			Name:      filepath.Base(exe),
			Path:      exe,
			Instances: 1,
			System:    uint32(pid) == 1 || pidOwner(entry.Name()) == 0,
		}
	}
	out := make([]Process, 0, len(byPath))
	for _, p := range byPath {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if a == b {
			return out[i].PID < out[j].PID
		}
		return a < b
	})
	return out
}

// ImagePath resolves a pid to its executable, or "" when it cannot be read.
func ImagePath(pid uint32) string {
	exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(exe, " (deleted)")
}

// NameOf is the executable name a rule matches for a pid. The Linux engine
// matches the image name it reads from the kernel, which is this basename.
func NameOf(pid uint32) string {
	if exe := ImagePath(pid); exe != "" {
		return filepath.Base(exe)
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// IconDataURL has no Linux equivalent: the picker draws its fallback glyph.
func IconDataURL(path string) string { return "" }

// pidOwner reads the uid that owns /proc/<pid>, which is the process owner.
func pidOwner(pid string) uint32 {
	fi, err := os.Stat(filepath.Join("/proc", pid))
	if err != nil {
		return ^uint32(0)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Uid
	}
	return ^uint32(0)
}
