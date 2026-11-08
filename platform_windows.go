//go:build windows

package main

import (
	"errors"
	"os/exec"
	"path/filepath"
)

// engineSubdir is where the bundled Windows engine sits inside resources/.
var engineSubdir = filepath.Join("engine", "win", "amd64")

// engineMarker is the file that must exist for a folder to count as the engine.
const engineMarker = "ProxyBridgeSvc.exe"

// engineFallbacks are extra places an installed engine may live.
func engineFallbacks() []string { return nil }

// openFolder shows a directory in Explorer.
func openFolder(path string) error { return exec.Command("explorer.exe", path).Start() }

// errServiceNotInstalled explains a failed post-elevation check.
func errServiceNotInstalled() error {
	return errors.New("服务未安装：请查看 %TEMP%\\ferry-install.log")
}
