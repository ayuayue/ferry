//go:build windows

package svc

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/ayuayue/ferry/internal/procattr"
)

func TestOfficialRunningServiceBlocks(t *testing.T) {
	root := `C:\Program Files\ProxyBridge`
	path := filepath.Join(root, "ProxyBridgeSvc.exe")
	err := findOfficialConflict([]string{root}, Status{
		Installed: true,
		Running:   true,
		BinPath:   `"` + path + `"`,
	}, nil)
	var conflict *OfficialConflict
	if !errors.As(err, &conflict) || conflict.Source != "引擎服务 ProxyBridgeSvc" {
		t.Fatalf("official running service: %#v", err)
	}
}

func TestFerryEngineAndStoppedOfficialDoNotBlock(t *testing.T) {
	root := `C:\Program Files\ProxyBridge`
	ferry := `D:\project\github\pbdeck\.mygo\dev\windows-amd64\engine\win\amd64\ProxyBridgeSvc.exe`
	processes := []procattr.Process{
		{Name: "ProxyBridgeSvc.exe", Path: ferry},
		{Name: "ProxyBridge.exe", Path: `C:\Tools\PortableProxyBridge\ProxyBridge.exe`},
	}
	if err := findOfficialConflict([]string{root}, Status{Installed: true, Running: true, BinPath: ferry}, processes); err != nil {
		t.Fatalf("Ferry engine must not block itself: %v", err)
	}
	official := filepath.Join(root, "ProxyBridgeSvc.exe")
	if err := findOfficialConflict([]string{root}, Status{Installed: true, Running: false, BinPath: official}, nil); err != nil {
		t.Fatalf("stopped official service may be replaced: %v", err)
	}
}

func TestOfficialGUIAndServicePathBoundaries(t *testing.T) {
	root := `C:\Program Files\ProxyBridge`
	path := filepath.Join(root, "ProxyBridge.exe")
	err := findOfficialConflict([]string{root}, Status{}, []procattr.Process{{Name: "ProxyBridge.exe", Path: path}})
	var conflict *OfficialConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("official GUI was not identified: %v", err)
	}
	if isOfficialPath(`C:\Program Files\ProxyBridge-old\ProxyBridge.exe`, []string{root}) {
		t.Fatal("a directory sharing the official prefix is not the official installation")
	}
	if p := serviceExecutable(`"C:\Program Files\ProxyBridge\ProxyBridgeSvc.exe"`); p != path[:len(path)-len("ProxyBridge.exe")]+"ProxyBridgeSvc.exe" {
		t.Fatalf("serviceExecutable = %q", p)
	}
}
