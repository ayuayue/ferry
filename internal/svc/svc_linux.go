//go:build linux

// Package svc owns the engine service on Linux.
//
// The Windows version registers ProxyBridgeSvc with the SCM and hands it a
// driver. On Linux the same engine runs as a systemd unit, and access to its
// control socket is decided by the `proxybridge` group, so Ferry installs its
// own unit (ferry-engine.service) instead of touching an official
// proxybridge.service that a user may already have.
package svc

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// Name is the unit Ferry installs, enables and removes.
	Name = "ferry-engine.service"
	// OfficialName is the unit an official ProxyBridge install creates.
	OfficialName = "proxybridge.service"
	// Group may control the engine socket.
	Group = "proxybridge"
	// Socket is the engine's control socket.
	Socket = "/run/proxybridge/ctl.sock"
	// EngineDir is where Install puts the engine binary.
	EngineDir = "/usr/lib/ferry/engine"
	// Binary is the engine executable's name.
	Binary = "ProxyBridge"

	unitPath = "/etc/systemd/system/" + Name
	confDir  = "/etc/proxybridge"
)

// EngineFiles are the files Install copies out of the bundled engine folder.
// The daemon needs nothing else: it links only against libc, the eBPF datapath
// is built in.
var EngineFiles = []string{Binary}

// Status is what the settings page shows about the engine service. The field
// names match the Windows build so the page needs no platform branch.
type Status struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	StartType string `json:"startType"`
	BinPath   string `json:"binPath"`
	Error     string `json:"error"`
}

var errNeedRoot = errors.New("需要管理员权限：请通过系统的授权提示执行此操作")

// IsAdmin reports whether the process can talk to systemd.
func IsAdmin() bool { return os.Geteuid() == 0 }

// Query reads the unit's state without disturbing it.
func Query() Status {
	var st Status
	unit, err := os.ReadFile(unitPath)
	if err != nil {
		st.Error = ""
		if !errors.Is(err, os.ErrNotExist) {
			st.Error = err.Error()
		}
		return st
	}
	st.Installed = true
	st.BinPath = execStart(string(unit))
	switch {
	case isActive(Name):
		st.Running = true
	case !hasSystemctl():
		st.Error = "找不到 systemctl，请在 systemd 系统上使用"
	}
	switch isEnabled(Name) {
	case "enabled":
		st.StartType = "enabled"
	case "static":
		st.StartType = "static"
	default:
		st.StartType = "disabled"
	}
	return st
}

// Install copies the bundled engine into place, registers the unit and starts
// it. It must run as root: the app re-launches itself through the user's
// privilege prompt, exactly like the Windows build does with UAC.
func Install(dir string) error {
	if !IsAdmin() {
		return errNeedRoot
	}
	if err := guardOfficialInstall(); err != nil {
		return err
	}
	bin := filepath.Join(dir, Binary)
	if _, err := os.Stat(bin); err != nil {
		return fmt.Errorf("找不到内置引擎 %s", bin)
	}
	if err := os.MkdirAll(EngineDir, 0o755); err != nil {
		return fmt.Errorf("创建引擎目录：%w", err)
	}
	// The running daemon keeps its binary mapped, so an upgrade must stop it
	// before the file can be replaced.
	if Query().Running {
		_, _ = systemctl("stop", Name)
	}
	for _, name := range EngineFiles {
		src := filepath.Join(dir, name)
		dst := filepath.Join(EngineDir, name)
		if src == dst {
			continue
		}
		mode := os.FileMode(0o644)
		if name == Binary {
			mode = 0o755
		}
		if err := copyFile(src, dst, mode); err != nil {
			return fmt.Errorf("复制 %s：%w", name, err)
		}
	}
	if err := os.MkdirAll(confDir, 0o700); err != nil {
		return fmt.Errorf("创建配置目录：%w", err)
	}
	if err := ensureGroup(); err != nil {
		return err
	}
	if u := installUser(); u != "" {
		if err := addToGroup(u); err != nil {
			return err
		}
	}

	unit, err := unitText(filepath.Join(EngineDir, Binary))
	if err != nil {
		return err
	}
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return fmt.Errorf("写入 unit：%w", err)
	}
	if _, err := systemctl("daemon-reload"); err != nil {
		return err
	}
	if _, err := systemctl("enable", "--now", Name); err != nil {
		return err
	}
	waitForSocket(5 * time.Second)
	return nil
}

// Uninstall stops and removes Ferry's own unit. An official install is left
// alone: Ferry only ever removes what it created.
func Uninstall() error {
	if !IsAdmin() {
		return errNeedRoot
	}
	_, _ = systemctl("disable", "--now", Name)
	if err := os.Remove(unitPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := systemctl("daemon-reload"); err != nil {
		return err
	}
	return nil
}

// Elevate re-runs the app in its install/uninstall mode with root rights.
func Elevate(exe string, args ...string) error {
	if _, err := exec.LookPath("pkexec"); err == nil {
		cmd := exec.Command("pkexec", append([]string{exe}, args...)...)
		cmd.Env = append(os.Environ(), "FERRY_INSTALL_USER="+currentUser())
		out, err := cmd.CombinedOutput()
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = err.Error()
			}
			return fmt.Errorf("授权安装引擎失败：%s", msg)
		}
		return nil
	}
	return fmt.Errorf("没有可用的图形授权工具（pkexec）。请在终端执行：sudo %s %s", exe, strings.Join(args, " "))
}

// CheckOfficialConflict refuses to start while an official ProxyBridge is
// running, so two engines never fight over the same eBPF hooks and socket.
func CheckOfficialConflict() error {
	var found []string
	if isActive(OfficialName) {
		found = append(found, "systemd 单元 "+OfficialName+" 正在运行")
	}
	for _, p := range officialProcesses() {
		found = append(found, fmt.Sprintf("%s（PID %d）", p.path, p.pid))
	}
	if len(found) == 0 {
		return nil
	}
	return fmt.Errorf("检测到官方的 ProxyBridge 正在运行：%s。请先停止它（sudo systemctl stop proxybridge），再启动 Ferry。",
		strings.Join(found, "、"))
}

// guardOfficialInstall runs before Install: an official unit that is enabled
// would start again at the next boot and replace the socket under Ferry.
func guardOfficialInstall() error {
	if isActive(OfficialName) {
		return fmt.Errorf("官方的 %s 正在运行，请先停止它（sudo systemctl stop proxybridge）", OfficialName)
	}
	if isEnabled(OfficialName) == "enabled" {
		return fmt.Errorf("官方的 %s 已设为开机启动，会与 Ferry 的引擎抢占同一套 eBPF 钩子。请先执行 sudo systemctl disable proxybridge，再安装 Ferry 的引擎。", OfficialName)
	}
	return nil
}

// ShowStartupError surfaces a startup block without a window, on a desktop
// session through a notification and otherwise on stderr.
func ShowStartupError(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	if _, err := exec.LookPath("notify-send"); err == nil {
		_ = exec.Command("notify-send", "--urgency=critical", "Ferry 无法启动", msg).Run()
	}
}

// ---- helpers ----

type procInfo struct {
	pid  int
	path string
}

// officialProcesses finds ProxyBridge programs that are not Ferry's own engine.
func officialProcesses() []procInfo {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	own := map[string]bool{
		filepath.Join(EngineDir, Binary): true,
	}
	if exe, err := os.Executable(); err == nil {
		own[filepath.Join(filepath.Dir(exe), "resources", "engine", "linux", "amd64", Binary)] = true
		own[filepath.Join(filepath.Dir(exe), "engine", "linux", "amd64", Binary)] = true
	}
	var out []procInfo
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		exe, err := os.Readlink(filepath.Join("/proc", entry.Name(), "exe"))
		if err != nil {
			continue
		}
		exe = strings.TrimSuffix(exe, " (deleted)")
		base := filepath.Base(exe)
		if base != Binary && base != "ProxyBridgeGUI" {
			continue
		}
		if own[exe] {
			continue
		}
		out = append(out, procInfo{pid: pid, path: exe})
	}
	return out
}

// unitTemplate. The engine ships its own unit via --print-unit, but that one
// sets ProtectSystem=strict together with ProtectKernelTunables,
// ProtectControlGroups, PrivateDevices and RestrictNamespaces. With those the
// service starts and reports an eBPF datapath yet never sees a connection: the
// loader cannot attach from inside that sandbox. This keeps the capability
// bounding set, the runtime directory and the restart policy, and drops only
// the restrictions that break the datapath.
const unitTemplate = `[Unit]
Description=Ferry engine (ProxyBridge)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%q --daemon
Restart=on-failure
RestartSec=5
RuntimeDirectory=proxybridge
RuntimeDirectoryMode=0755
LimitNOFILE=1048576
CapabilityBoundingSet=CAP_BPF CAP_PERFMON CAP_NET_ADMIN CAP_SYS_ADMIN CAP_SYS_RESOURCE CAP_SYS_PTRACE CAP_DAC_READ_SEARCH
NoNewPrivileges=yes
PrivateTmp=yes
ProtectHome=yes
ProtectSystem=full
ReadWritePaths=/etc/proxybridge -/sys/fs/bpf
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK
MemoryMax=1G
TasksMax=512
UMask=0077

[Install]
WantedBy=multi-user.target
`

func unitText(bin string) (string, error) {
	if bin == "" {
		return "", errors.New("引擎路径为空")
	}
	return fmt.Sprintf(unitTemplate, bin), nil
}

func execStart(unit string) string {
	for _, line := range strings.Split(unit, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "ExecStart=") {
			continue
		}
		v := strings.TrimPrefix(line, "ExecStart=")
		if i := strings.IndexByte(v, '"'); i >= 0 {
			if j := strings.IndexByte(v[i+1:], '"'); j >= 0 {
				return v[i+1 : i+1+j]
			}
		}
		if f := strings.Fields(v); len(f) > 0 {
			return f[0]
		}
	}
	return ""
}

func ensureGroup() error {
	if _, err := user.LookupGroup(Group); err == nil {
		return nil
	}
	if _, err := exec.LookPath("groupadd"); err != nil {
		return fmt.Errorf("找不到 groupadd，无法创建 %s 组", Group)
	}
	if out, err := exec.Command("groupadd", "--system", Group).CombinedOutput(); err != nil {
		return fmt.Errorf("创建 %s 组：%v: %s", Group, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// addToGroup is additive: it never removes the user from other groups.
func addToGroup(name string) error {
	if _, err := exec.LookPath("usermod"); err != nil {
		return fmt.Errorf("找不到 usermod，无法把 %s 加入 %s 组", name, Group)
	}
	if out, err := exec.Command("usermod", "-aG", Group, name).CombinedOutput(); err != nil {
		return fmt.Errorf("把 %s 加入 %s 组：%v: %s", name, Group, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// installUser is the desktop user that asked for the installation. The engine
// checks group membership from /etc/group on every connect, so membership takes
// effect immediately, without the user logging out.
func installUser() string {
	if v := strings.TrimSpace(os.Getenv("FERRY_INSTALL_USER")); v != "" && v != "root" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("SUDO_USER")); v != "" && v != "root" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("PKEXEC_UID")); v != "" {
		if u, err := user.LookupId(v); err == nil && u.Username != "root" {
			return u.Username
		}
	}
	return ""
}

func currentUser() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return ""
}

func hasSystemctl() bool {
	_, err := exec.LookPath("systemctl")
	return err == nil
}

func systemctl(args ...string) (string, error) {
	if !hasSystemctl() {
		return "", errors.New("找不到 systemctl")
	}
	cmd := exec.Command("systemctl", args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return text, fmt.Errorf("systemctl %s：%s", strings.Join(args, " "), text)
	}
	return text, nil
}

func isActive(unit string) bool {
	if !hasSystemctl() {
		return false
	}
	return exec.Command("systemctl", "is-active", "--quiet", unit).Run() == nil
}

func isEnabled(unit string) string {
	if !hasSystemctl() {
		return ""
	}
	out, err := exec.Command("systemctl", "is-enabled", unit).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// waitForSocket gives the daemon a moment to create its control socket, so the
// app connects straight away instead of failing once and retrying.
func waitForSocket(d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(Socket); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}
