//go:build windows

package svc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"

	"github.com/ayuayue/ferry/internal/procattr"
)

const officialInstallKey = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\ProxyBridge`

// OfficialConflict describes a running official ProxyBridge instance. A service
// name alone cannot identify it: Ferry installs the same upstream engine.
type OfficialConflict struct {
	Source  string
	Process string
	Path    string
}

func (e *OfficialConflict) Error() string {
	return fmt.Sprintf("检测到官方 ProxyBridge 正在运行（%s）。\n\n请完全退出官方应用并停止其引擎服务，然后重新启动 Ferry。Ferry 不会自动结束官方进程。\n\n%s", e.Source, e.Path)
}

// CheckOfficialConflict reports a running official GUI, CLI or engine. Official
// software that is installed but stopped does not block Ferry.
func CheckOfficialConflict() error {
	return findOfficialConflict(officialInstallDirs(), Query(), procattr.List())
}

// guardOfficialInstall is also used by Install, before it stops or replaces a
// stopped official service. Keeping both paths protected closes the elevated
// command-line route into the app's own settings action.
func guardOfficialInstall() error { return CheckOfficialConflict() }

func findOfficialConflict(dirs []string, service Status, processes []procattr.Process) error {
	for _, p := range processes {
		switch strings.ToLower(p.Name) {
		case "proxybridge.exe", "proxybridge_cli.exe", "proxybridgesvc.exe":
			if isOfficialPath(p.Path, dirs) {
				return &OfficialConflict{Source: p.Name, Process: p.Name, Path: p.Path}
			}
		}
	}
	if service.Running && isOfficialPath(serviceExecutable(service.BinPath), dirs) {
		return &OfficialConflict{Source: "引擎服务 ProxyBridgeSvc", Process: "ProxyBridgeSvc.exe", Path: serviceExecutable(service.BinPath)}
	}
	return nil
}

func officialInstallDirs() []string {
	var dirs []string
	for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		if key, err := registry.OpenKey(root, officialInstallKey, registry.QUERY_VALUE|registry.WOW64_64KEY); err == nil {
			if dir, _, err := key.GetStringValue("InstallLocation"); err == nil && dir != "" {
				dirs = append(dirs, dir)
			}
			key.Close()
		}
	}
	if dir := os.Getenv("ProgramFiles"); dir != "" {
		dirs = append(dirs, filepath.Join(dir, "ProxyBridge"))
	}
	return uniquePaths(dirs)
}

func isOfficialPath(path string, dirs []string) bool {
	if path == "" {
		return false
	}
	// procattr returns a Win32 path. Convert an extended-length service path to
	// the same form before applying filepath.Rel.
	path = strings.TrimPrefix(strings.TrimSpace(path), `\\?\`)
	path = strings.TrimPrefix(path, `\??\`)
	for _, dir := range dirs {
		rel, err := filepath.Rel(dir, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			return true
		}
	}
	return false
}

func serviceExecutable(binPath string) string {
	s := os.ExpandEnv(strings.TrimSpace(binPath))
	if strings.HasPrefix(s, `"`) {
		if end := strings.Index(s[1:], `"`); end >= 0 {
			return s[1 : end+1]
		}
	}
	if end := strings.IndexAny(s, " \t"); end >= 0 {
		s = s[:end]
	}
	return strings.Trim(s, `"`)
}

func uniquePaths(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	out := paths[:0]
	for _, path := range paths {
		path = filepath.Clean(strings.TrimSpace(path))
		key := strings.ToLower(path)
		if path != "." && path != "" && !seen[key] {
			seen[key] = true
			out = append(out, path)
		}
	}
	return out
}
