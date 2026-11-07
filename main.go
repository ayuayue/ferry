package main

import (
	_ "embed"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/egoist/mygo"

	"pbdeck/internal/model"
	"pbdeck/internal/procattr"
	"pbdeck/internal/session"
	"pbdeck/internal/svc"
)

const version = "0.1.0"

//go:embed resources/icon.png
var appIcon []byte

//go:embed resources/tray.png
var trayIcon []byte

// StatusView is what the settings page shows about the machine.
type StatusView struct {
	Service      svc.Status `json:"service"`
	Admin        bool       `json:"admin"`
	DataDir      string     `json:"dataDir"`
	SettingsPath string     `json:"settingsPath"`
	EngineDir    string     `json:"engineDir"`
	EngineReady  bool       `json:"engineReady"`
	AutoStart    bool       `json:"autoStart"`
	Version      string     `json:"version"`
	Platform     string     `json:"platform"`
}

// API is the service the page calls. Every method may be called from the page
// and runs on its own goroutine.
type API struct {
	store *model.Store
	sess  *session.Session
}

// View returns the whole UI state plus the events since `since`.
func (a *API) View(since uint64) session.View { return a.sess.Snapshot(since) }

// Connect dials the engine service and replays the stored rules.
func (a *API) Connect() error {
	return a.sess.Connect()
}

// Disconnect drops the engine connection.
func (a *API) Disconnect() { a.sess.Disconnect() }

// SetEnabled pauses or resumes rule matching.
func (a *API) SetEnabled(on bool) error { return a.sess.SetEnabled(on) }

// SetTrafficLog toggles the engine's packet log.
func (a *API) SetTrafficLog(on bool) error { return a.sess.SetTrafficLog(on) }

// SetLogAll logs every connection instead of collapsing repeats.
func (a *API) SetLogAll(on bool) error { return a.sess.SetLogAll(on) }

// ClearEvents empties the activity list.
func (a *API) ClearEvents() error { return a.sess.ClearEvents() }

// SaveProxy adds or updates a proxy configuration.
func (a *API) SaveProxy(p model.Proxy) (model.Proxy, error) { return a.sess.SaveProxy(p) }

// DeleteProxy removes a proxy configuration.
func (a *API) DeleteProxy(key string) error { return a.sess.DeleteProxy(key) }

// TestProxy streams a connectivity test for one proxy.
func (a *API) TestProxy(key string) error { return a.sess.TestProxy(key) }

// SaveRule adds or updates a rule; insertAt is 1-based (0 appends).
func (a *API) SaveRule(r model.Rule, insertAt int) (model.Rule, error) {
	return a.sess.SaveRule(r, insertAt)
}

// DeleteRule removes a rule.
func (a *API) DeleteRule(key string) error { return a.sess.DeleteRule(key) }

// ToggleRule enables or disables a rule.
func (a *API) ToggleRule(key string, on bool) error { return a.sess.ToggleRule(key, on) }

// ReorderRules applies a new top-to-bottom order.
func (a *API) ReorderRules(keys []string) error { return a.sess.Reorder(keys) }

// RulesFromTargets turns selected connections or log lines into rules.
func (a *API) RulesFromTargets(targets []session.RuleTarget, opts session.RuleOpts) ([]session.RuleResult, error) {
	return a.sess.RulesFromTargets(targets, opts)
}

// Processes lists running programs for the rule editor's picker.
func (a *API) Processes() []procattr.Process { return procattr.List() }

// Icon returns a data URL for an executable's icon (cached).
func (a *API) Icon(path string) string { return procattr.IconDataURL(path) }

// Status reports service, elevation and paths.
func (a *API) Status() StatusView {
	dir := engineDir()
	ready := true
	for _, f := range svc.EngineFiles {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			ready = false
			break
		}
	}
	return StatusView{
		Service:      svc.Query(),
		Admin:        svc.IsAdmin(),
		DataDir:      dataDir(),
		SettingsPath: a.store.Path(),
		EngineDir:    dir,
		EngineReady:  ready,
		AutoStart:    mygo.App.OpenAtLogin(),
		Version:      version,
		Platform:     runtime.GOOS + "/" + runtime.GOARCH,
	}
}

// InstallEngine registers the bundled engine service. It re-launches itself
// elevated when the app is not administrator.
func (a *API) InstallEngine() error {
	if !svc.IsAdmin() {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		if err := svc.Elevate(exe, "--install-engine"); err != nil {
			return err
		}
		if st := svc.Query(); !st.Installed {
			return errors.New("服务未安装：请查看 %TEMP%\\pbdeck-install.log")
		}
		return nil
	}
	return svc.Install(engineDir())
}

// UninstallEngine stops and removes the service.
func (a *API) UninstallEngine() error {
	if !svc.IsAdmin() {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		return svc.Elevate(exe, "--uninstall-engine")
	}
	return svc.Uninstall()
}

// SaveUI remembers the last page, theme and toggles.
func (a *API) SaveUI(ui model.UIState) error {
	return a.store.Update(func(st *model.Settings) error {
		st.UI = ui
		return nil
	})
}

// SetAutoStart adds or removes the current user's login item.
func (a *API) SetAutoStart(on bool) error {
	if err := mygo.App.SetOpenAtLogin(on); err != nil {
		return err
	}
	return a.store.Update(func(st *model.Settings) error {
		st.Engine.AutoStart = on
		return nil
	})
}

// RevealData opens the data directory in Explorer.
func (a *API) RevealData() error {
	return exec.Command("explorer.exe", dataDir()).Start()
}

// RevealEngine opens the folder holding the engine binaries.
func (a *API) RevealEngine() error {
	return exec.Command("explorer.exe", engineDir()).Start()
}

func dataDir() string {
	if dir, err := mygo.App.Path(mygo.PathUserData); err == nil && dir != "" {
		return dir
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "PBDeck")
	}
	return "."
}

// engineDir finds the bundled engine next to the executable, in the source
// tree during `mygo dev`, or in the installed resources directory.
func engineDir() string {
	sep := filepath.Join("engine", "win", "amd64")
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		base := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(base, "resources", sep),
			filepath.Join(base, sep),
		)
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, "resources", sep))
	}
	if p, err := mygo.App.Path(mygo.PathResources); err == nil && p != "" {
		candidates = append(candidates, filepath.Join(p, sep))
	}
	for _, dir := range candidates {
		if _, err := os.Stat(filepath.Join(dir, "ProxyBridgeSvc.exe")); err == nil {
			return dir
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return sep
}

// runElevatedTask handles the --install-engine/--uninstall-engine child modes
// before any window is created.
func runElevatedTask(arg string) bool {
	switch arg {
	case "--install-engine", "--uninstall-engine":
		var err error
		if arg == "--install-engine" {
			err = svc.Install(engineDir())
		} else {
			err = svc.Uninstall()
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	return false
}

func main() {
	for _, arg := range os.Args[1:] {
		runElevatedTask(arg)
	}

	store, err := model.Open(filepath.Join(dataDir(), "settings.json"))
	if err != nil {
		log.Fatalf("settings: %v", err)
	}
	sess := session.New(store, log.Printf)

	api := &API{store: store, sess: sess}
	mygo.Bind(api)

	mygo.App.WhenReady(func() {
		launchedAtLogin := mygo.App.WasOpenedAtLogin()
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:           "PBDeck",
			Width:           1240,
			Height:          800,
			MinWidth:        980,
			MinHeight:       600,
			Frameless:       true,
			CornerRadius:    12,
			BackgroundColor: "light-dark(#f4f5f7, #0d0f13)",
			StateKey:        "main",
			URL:             "/",
			Hidden:          launchedAtLogin,
		})

		quitting := false
		mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { quitting = true })

		if err := win.SetIcon(appIcon); err != nil {
			log.Printf("window icon: %v", err)
		}

		focus := func() {
			win.Show()
			win.Focus()
		}
		tray, err := mygo.NewTray(mygo.TrayOptions{
			Icon:    trayIcon,
			ToolTip: "PBDeck — 代理规则控制台",
			Menu: mygo.NewMenu([]*mygo.MenuItem{
				{Label: "显示 PBDeck", Click: func(*mygo.MenuItem, *mygo.Window) { focus() }},
				mygo.Separator(),
				{Label: "退出", Role: mygo.RoleQuit},
			}),
		})
		if err != nil {
			log.Printf("tray: %v", err)
		} else {
			tray.OnClick(focus)
		}

		win.OnClose(func(e *mygo.CloseEvent) {
			if quitting || tray == nil {
				sess.Disconnect()
				return
			}
			e.PreventDefault()
			win.Hide()
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
