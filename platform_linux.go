//go:build linux

package main

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/ayuayue/ferry/internal/svc"
)

// engineSubdir is where the bundled Linux engine sits inside resources/.
var engineSubdir = filepath.Join("engine", "linux", "amd64")

// engineMarker is the file that must exist for a folder to count as the engine.
const engineMarker = svc.Binary

// engineFallbacks also looks at the location the .deb installs the engine to,
// so the packaged app finds it without a resources copy next to the binary.
func engineFallbacks() []string { return []string{svc.EngineDir} }

// openFolder shows a directory in the desktop's file manager.
func openFolder(path string) error {
	if _, err := exec.LookPath("xdg-open"); err != nil {
		return errors.New("找不到 xdg-open，无法打开文件管理器")
	}
	return exec.Command("xdg-open", path).Start()
}

// errServiceNotInstalled explains a failed post-elevation check.
func errServiceNotInstalled() error {
	return fmt.Errorf("引擎服务未运行：请执行 sudo systemctl status %s 查看原因", svc.Name)
}
