// Package svc installs and inspects the privileged engine service that Ferry
// drives. The service is the unmodified ProxyBridge engine: it owns
// ProxyBridgeCore.dll and WinDivert, and the app talks to it over named pipes.
package svc

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Name is the service name the engine hard-codes in its service dispatcher, so
// it cannot be renamed.
const Name = "ProxyBridgeSvc"

// SDDL mirrors the upstream installer: SYSTEM and Administrators full control,
// interactive users may start it (the pipe DACL does the rest).
const SDDL = "D:(A;;CCLCSWRPWPDTLOCRRC;;;SY)(A;;CCDCLCSWRPWPDTLOCRSDRCWDWO;;;BA)(A;;CCLCSWRPLOCRRC;;;IU)(A;;CCLCSWLOCRRC;;;SU)"

// Status describes the service as the SCM sees it.
type Status struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	StartType string `json:"startType"`
	BinPath   string `json:"binPath"`
	Error     string `json:"error,omitempty"`
}

// IsAdmin reports whether this process can create services.
func IsAdmin() bool {
	t := windows.GetCurrentProcessToken()
	return t.IsElevated()
}

// Query reads the service state without needing elevation. It talks to the SCM
// with read-only rights: mgr.Connect asks for SC_MANAGER_ALL_ACCESS and is
// denied for unelevated callers.
func Query() Status {
	var st Status
	m, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	defer windows.CloseServiceHandle(m)

	s, err := windows.OpenService(m, windows.StringToUTF16Ptr(Name), windows.SERVICE_QUERY_STATUS)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return st
		}
		st.Error = err.Error()
		return st
	}
	defer windows.CloseServiceHandle(s)

	st.Installed = true
	var status windows.SERVICE_STATUS
	if err := windows.QueryServiceStatus(s, &status); err == nil {
		st.Running = status.CurrentState == windows.SERVICE_RUNNING
	}
	st.StartType, st.BinPath = config()
	return st
}

// config reads what the installer wrote to the registry instead of asking the
// SCM for SERVICE_QUERY_CONFIG, which keeps the whole call read-only.
func config() (startType, binPath string) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Services\`+Name, registry.QUERY_VALUE)
	if err != nil {
		return "", ""
	}
	defer k.Close()
	if v, _, err := k.GetIntegerValue("Start"); err == nil {
		switch v {
		case 2:
			startType = "auto"
		case 3:
			startType = "manual"
		case 4:
			startType = "disabled"
		default:
			startType = fmt.Sprintf("%d", v)
		}
	}
	if v, _, err := k.GetStringValue("ImagePath"); err == nil {
		binPath = v
	}
	return startType, binPath
}

// EngineFiles are the binaries the service needs next to it.
var EngineFiles = []string{"ProxyBridgeCore.dll", "ProxyBridgeSvc.exe", "WinDivert.dll", "WinDivert64.sys"}

// Install registers and starts the service from dir. It must run elevated.
// The steps mirror the upstream NSIS installer.
func Install(dir string) error {
	if err := guardOfficialInstall(); err != nil {
		return err
	}
	for _, f := range EngineFiles {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			return fmt.Errorf("engine file missing: %s", f)
		}
	}
	if !IsAdmin() {
		return errors.New("service installation needs administrator rights")
	}
	exe := filepath.Join(dir, "ProxyBridgeSvc.exe")

	logf, _ := os.OpenFile(filepath.Join(os.TempDir(), "ferry-engine-install.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	logStep := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		if logf != nil {
			fmt.Fprintln(logf, line)
		}
	}

	// A previous engine (the vendor's own installer uses the same name) must
	// not keep the pipes or the driver.
	logStep("--- install %s", dir)
	run := func(name string, args ...string) (string, error) {
		cmd := exec.Command(name, args...)
		out, err := cmd.CombinedOutput()
		logStep("%s %s -> %v\n%s", name, strings.Join(args, " "), err, out)
		return string(out), err
	}

	run("sc.exe", "stop", Name) // may fail when absent; that is fine
	run("sc.exe", "delete", Name)
	if _, err := run("sc.exe", "create", Name, "binPath=", `"`+exe+`"`, "start=", "auto", "DisplayName=", "Ferry Engine"); err != nil {
		return fmt.Errorf("sc create: %w", err)
	}
	run("sc.exe", "description", Name, "Privileged traffic-redirection engine for Ferry (ProxyBridge core).")
	run("sc.exe", "sdset", Name, SDDL)

	// Grants the console user access to the pipes and writes its SID for the
	// service's DACL.
	if _, err := run(exe, "--setup-users"); err != nil {
		logStep("setup-users failed (continuing): %v", err)
	}
	if _, err := run("sc.exe", "start", Name); err != nil {
		return fmt.Errorf("sc start: %w", err)
	}
	return nil
}

// Uninstall stops and removes the service. It must run elevated.
func Uninstall() error {
	if !IsAdmin() {
		return errors.New("service removal needs administrator rights")
	}
	exec.Command("sc.exe", "stop", Name).Run()
	out, err := exec.Command("sc.exe", "delete", Name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("sc delete: %v: %s", err, strings.TrimSpace(string(out)))
	}
	exec.Command("sc.exe", "stop", "WinDivert").Run()
	exec.Command("sc.exe", "delete", "WinDivert").Run()
	return nil
}

// Elevate re-runs this executable with args through the UAC prompt and waits
// for it. It returns once the elevated process exited; a declined prompt is an
// error.
func Elevate(exe string, args ...string) error {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = "'" + strings.ReplaceAll(a, "'", "''") + "'"
	}
	script := fmt.Sprintf(
		"$p = Start-Process -FilePath '%s' -ArgumentList %s -Verb RunAs -PassThru -Wait; exit $p.ExitCode",
		strings.ReplaceAll(exe, "'", "''"), strings.Join(quoted, ","))
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return fmt.Errorf("elevation failed: %w", err)
		}
		return fmt.Errorf("elevation failed: %s", msg)
	}
	return nil
}
