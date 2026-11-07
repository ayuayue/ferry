// Package procattr lists running programs and their icons, so a rule can be
// aimed at an app by picking it instead of typing its executable name.
package procattr

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Process is one running program, deduplicated by executable path.
type Process struct {
	PID      uint32 `json:"pid"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	Instances int   `json:"instances"`
	System   bool   `json:"system"`
}

var errNoIcon = errors.New("procattr: no icon")

// List returns the running programs that own a visible executable, deduped by
// path and sorted by name. It never fails: entries it cannot inspect are left
// out.
func List() []Process {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snap, &entry); err != nil {
		return nil
	}

	byPath := make(map[string]*Process, 256)
	for {
		pid := entry.ProcessID
		name := windows.UTF16ToString(entry.ExeFile[:])
		if pid > 4 && name != "" && !strings.EqualFold(name, "System") {
			path, err := ImagePath(pid)
			if err == nil && path != "" {
				if p, ok := byPath[strings.ToLower(path)]; ok {
					p.Instances++
				} else {
					byPath[strings.ToLower(path)] = &Process{
						PID:       pid,
						Name:      filepath.Base(path),
						Path:      path,
						Instances: 1,
						System:    isSystemPath(path),
					}
				}
			}
		}
		if err := windows.Process32Next(snap, &entry); err != nil {
			break
		}
	}

	out := make([]Process, 0, len(byPath))
	for _, p := range byPath {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].System != out[j].System {
			return !out[i].System
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func isSystemPath(path string) bool {
	lower := strings.ToLower(path)
	if strings.Contains(lower, `\windows\system32\`) || strings.Contains(lower, `\windows\syswow64\`) {
		return true
	}
	// WindowsApps holds Store apps; their executables are rarely a target.
	return strings.Contains(lower, `\windowsapps\`)
}

// ImagePath returns the full path of a running process.
func ImagePath(pid uint32) (string, error) {
	const access = windows.PROCESS_QUERY_LIMITED_INFORMATION
	h, err := windows.OpenProcess(access, false, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)

	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf[:size]), nil
}

// NameOf returns the executable name (not the path) for a PID, which is what
// rules store.
func NameOf(pid uint32) string {
	path, err := ImagePath(pid)
	if err != nil {
		return ""
	}
	return filepath.Base(path)
}

var (
	user32  = windows.NewLazySystemDLL("user32.dll")
	shell32 = windows.NewLazySystemDLL("shell32.dll")
	gdi32   = windows.NewLazySystemDLL("gdi32.dll")

	procExtractIconExW      = shell32.NewProc("ExtractIconExW")
	procDestroyIcon         = user32.NewProc("DestroyIcon")
	procGetIconInfo         = user32.NewProc("GetIconInfo")
	procDrawIconEx          = user32.NewProc("DrawIconEx")
	procCreateCompatibleDC  = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection    = gdi32.NewProc("CreateDIBSection")
	procSelectObject        = gdi32.NewProc("SelectObject")
	procDeleteObject        = gdi32.NewProc("DeleteObject")
	procDeleteDC            = gdi32.NewProc("DeleteDC")
)

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]uint32
}

const (
	dibRGBColors = 0
	diNormal     = 3
	iconSize     = 32
)

var icons = struct {
	sync.Mutex
	m map[string]string
}{m: make(map[string]string, 128)}

// IconDataURL returns a PNG data URL of the file's icon, or "" when the shell
// has none. Results are cached by path.
func IconDataURL(path string) string {
	if path == "" {
		return ""
	}
	icons.Lock()
	if v, ok := icons.m[path]; ok {
		icons.Unlock()
		return v
	}
	icons.Unlock()

	url := ""
	if png, err := renderIcon(path); err == nil && len(png) > 0 {
		url = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	}
	icons.Lock()
	icons.m[path] = url
	icons.Unlock()
	return url
}

func renderIcon(path string) ([]byte, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	var large, small windows.Handle
	n, _, _ := procExtractIconExW.Call(uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&large)), uintptr(unsafe.Pointer(&small)), 1)
	if n == 0 || large == 0 {
		return nil, errNoIcon
	}
	defer procDestroyIcon.Call(uintptr(large))
	if small != 0 && small != large {
		defer procDestroyIcon.Call(uintptr(small))
	}

	dc, _, _ := procCreateCompatibleDC.Call(0)
	if dc == 0 {
		return nil, errNoIcon
	}
	defer procDeleteDC.Call(dc)

	bi := bitmapInfo{Header: bitmapInfoHeader{
		Size:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width:       iconSize,
		Height:      -iconSize, // top-down
		Planes:      1,
		BitCount:    32,
		Compression: 0, // BI_RGB
	}}
	var bits unsafe.Pointer
	hbmp, _, _ := procCreateDIBSection.Call(dc, uintptr(unsafe.Pointer(&bi)), dibRGBColors, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if hbmp == 0 || bits == nil {
		return nil, errNoIcon
	}
	defer procDeleteObject.Call(hbmp)

	old, _, _ := procSelectObject.Call(dc, hbmp)
	defer procSelectObject.Call(dc, old)

	ret, _, _ := procDrawIconEx.Call(dc, 0, 0, uintptr(large), iconSize, iconSize, 0, 0, diNormal)
	if ret == 0 {
		return nil, errNoIcon
	}

	src := unsafe.Slice((*byte)(bits), iconSize*iconSize*4)
	img := image.NewNRGBA(image.Rect(0, 0, iconSize, iconSize))
	const bg = 0 // composite onto transparent, then force opaque for dark UI
	_ = bg
	for i := 0; i < iconSize*iconSize; i++ {
		b, g, r, a := src[i*4], src[i*4+1], src[i*4+2], src[i*4+3]
		if a == 0 {
			// Icons drawn without an alpha channel come back fully
			// transparent; treat black-with-zero-alpha as opaque art.
			if r == 0 && g == 0 && b == 0 {
				a = 0
			} else {
				a = 255
			}
		}
		img.Pix[i*4] = r
		img.Pix[i*4+1] = g
		img.Pix[i*4+2] = b
		img.Pix[i*4+3] = a
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
