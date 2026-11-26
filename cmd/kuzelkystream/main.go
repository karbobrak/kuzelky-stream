// KuzelkyStream: camera → score graphics → YouTube. Controlled from the browser at http://127.0.0.1:<port>;
// an icon in the notification area opens the UI and quits the app.
//
// Release build (no console window):  go build -ldflags "-H=windowsgui" -o KuzelkyStream.exe ./cmd/kuzelkystream
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"kuzelkystream/internal/app"
	"kuzelkystream/internal/cka"
	"kuzelkystream/internal/config"
	"kuzelkystream/internal/stream"
	"kuzelkystream/internal/tray"
	"kuzelkystream/internal/update"
	"kuzelkystream/internal/web"
	"kuzelkystream/internal/win"
)

// Set at build time by build.ps1 (-ldflags -X): version and the Google OAuth client ("Desktop app")
// that is built into the exe, so clubs only sign in. For a desktop app Google does not treat the
// client secret as confidential; the user's own sign-in is what protects the account.
var (
	version           = "vývojová"
	oauthClientID     string
	oauthClientSecret string
)

// appDir is the folder with the exe; under `go run` (exe in a temp build dir) it is the working dir.
func appDir() string {
	exe, err := os.Executable()
	if err == nil && !strings.Contains(strings.ToLower(exe), "go-build") {
		return filepath.Dir(exe)
	}
	wd, _ := os.Getwd()
	return wd
}

func main() {
	dir := appDir()
	cfgPath := flag.String("config", filepath.Join(dir, "config.json"), "cesta k config.json")
	noBrowser := flag.Bool("bez-prohlizece", false, "neotevírat prohlížeč")
	noUpdate := flag.Bool("bez-aktualizace", false, "nekontrolovat novou verzi")
	afterUpdate := flag.Bool("po-aktualizaci", false, "spuštěno po aktualizaci (počkat na ukončení staré verze)")
	flag.Parse()
	portFile := filepath.Join(dir, "data", "port.txt")

	h, err := win.SingleInstance("KuzelkyStream")
	for i := 0; err != nil && *afterUpdate && i < 60; i++ { // the old version is still closing
		win.CloseHandle(h)
		time.Sleep(250 * time.Millisecond)
		h, err = win.SingleInstance("KuzelkyStream")
	}
	if err != nil {
		// Already running: just bring up its UI.
		if b, err := os.ReadFile(portFile); err == nil {
			if p, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				_ = win.OpenBrowser(fmt.Sprintf("http://127.0.0.1:%d/", p))
				return
			}
		}
		win.ShowInfo("Kuželky Stream už běží – ikona je v oznamovací oblasti vedle hodin.")
		return
	}

	logger, logBuf, closeLog := setupLog(dir)
	defer closeLog()

	// Keep the app current: a newer release replaces the exe and starts it again – before anything
	// else runs, so an update can never interrupt a broadcast.
	if exe, err := os.Executable(); err == nil {
		update.Cleanup(exe)
		if !*noUpdate && update.IsRelease(version) && selfUpdate(logger, exe) {
			return
		}
	}

	cfg, err := config.Load(*cfgPath)
	switch {
	case errors.Is(err, config.ErrCreated):
		logger.Printf("vytvořen %s s výchozím nastavením – doplňte ho na stránce Nastavení", *cfgPath)
	case err != nil:
		// A broken config must not lock the user out of the UI: start with defaults and say so.
		logger.Printf("CHYBA v %s: %v – používám výchozí nastavení, soubor se přepíše až uložením v Nastavení", *cfgPath, err)
		cfg = config.Default()
	}

	ffmpeg, err := stream.FindTool("ffmpeg")
	if err != nil {
		logger.Printf("CHYBA: %v", err)
		win.ShowError("Chybí FFmpeg – bez něj nelze vysílat.\n\nSoubor ffmpeg.exe patří do složky\n" +
			filepath.Join(dir, "ffmpeg"))
		return
	}

	a := app.New(dir, *cfgPath, cfg, ffmpeg, logger, logBuf)
	a.KeepAwake = win.KeepAwake
	a.Version = version
	a.UseBuiltinClient(oauthClientID, oauthClientSecret)
	cloudCtx, stopCloud := context.WithCancel(context.Background())
	go a.RunCloud(cloudCtx)
	cka.UserAgent = fmt.Sprintf("KuzelkyStream/%s (+https://github.com/karbobrak/kuzelky-stream)", version)
	go a.RunImport(cloudCtx)

	ln, port, err := web.Listen(cfg.App.Port)
	if err != nil {
		logger.Printf("nelze otevřít port %d: %v", cfg.App.Port, err)
		win.ShowError(fmt.Sprintf("Nelze otevřít port %d pro ovládání: %v", cfg.App.Port, err))
		return
	}
	_ = os.MkdirAll(filepath.Dir(portFile), 0o755)
	_ = os.WriteFile(portFile, []byte(strconv.Itoa(port)), 0o644)
	defer os.Remove(portFile)

	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	logger.Printf("KuzelkyStream %s běží: %s", version, url)
	logger.Printf("systém: Windows %s, %d jader, %s", win.Version(), runtime.NumCPU(), runtime.Version())
	go heartbeat(logger) // the last line before a hard crash shows when and how big the app was
	go func() {
		if err := web.Serve(ln, port, a); err != nil {
			logger.Printf("webový server: %v", err)
		}
	}()
	go a.AutoStart() // nonstop broadcasting: the alley goes on air as soon as the app starts
	openUI := func() { _ = win.OpenBrowser(url) }

	// Ctrl+C (console build) or Windows logoff/shutdown: stop cleanly so YouTube gets a proper end.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		tray.Quit()
	}()

	tray.Run(tray.Options{
		OnReady: func() {
			if cfg.App.OpenBrowser && !*noBrowser {
				openUI()
			}
		},
		OnOpen: openUI,
		OnQuit: func() bool {
			if a.Running() {
				return win.Confirm("Vysílání běží. Opravdu ukončit aplikaci a zastavit vysílání?")
			}
			return true
		},
		Status: func() (tray.State, string) {
			s := a.Status()
			switch {
			case !s.Running:
				return tray.Idle, "Kuželky Stream – nevysílá se"
			case s.Output.State != "bezi":
				return tray.Warn, "Kuželky Stream – obnovuji spojení"
			case s.ShowingSlate:
				return tray.Warn, "Kuželky Stream – vysílá se náhradní obraz (kamera nedostupná)"
			}
			return tray.Live, "Kuželky Stream – vysílá se"
		},
	})

	logger.Print("ukončuji…")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { a.Stop(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		logger.Print("vysílání se nepodařilo zastavit včas")
	}
	stopCloud()
	flushCtx, cancelFlush := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelFlush()
	a.FlushCloud(flushCtx) // last changes to Google Drive
}

// selfUpdate installs a newer release and starts it; true = this process should exit.
func selfUpdate(logger *log.Logger, exe string) bool {
	update.UserAgent = "KuzelkyStream/" + version
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	rel, err := update.Latest(ctx)
	cancel()
	if err != nil {
		logger.Printf("aktualizace: nelze zjistit novou verzi (%v) – pokračuji s %s", err, version)
		return false
	}
	if !update.Newer(rel.Tag, version) {
		logger.Printf("aktualizace: %s je nejnovější verze", version)
		return false
	}
	logger.Printf("aktualizace: stahuji verzi %s (běží %s)", rel.Version(), version)
	ctx, cancel = context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := update.Install(ctx, rel, exe); err != nil {
		logger.Printf("aktualizace se nepovedla: %v – pokračuji s %s", err, version)
		return false
	}
	args := append([]string{"-po-aktualizaci"}, os.Args[1:]...)
	cmd := exec.Command(exe, args...)
	cmd.Dir = filepath.Dir(exe)
	if err := cmd.Start(); err != nil {
		logger.Printf("nová verze se nespustila (%v) – spusťte aplikaci znovu", err)
		return false
	}
	logger.Printf("aktualizace: verze %s nainstalována, spouštím ji", rel.Version())
	return true
}

// setupLog writes to the console (if there is one), a file in logs\ (one per start) and the
// in-memory buffer for the UI.
func setupLog(dir string) (*log.Logger, *app.LogBuf, func()) {
	buf := app.NewLogBuf(1000)
	writers := []io.Writer{buf}
	// A GUI build has no console; writing to its stdout would fail and stop io.MultiWriter.
	if _, err := os.Stdout.Stat(); err == nil {
		writers = append(writers, os.Stdout)
	}
	closeFn := func() {}
	logDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err == nil {
		pruneLogs(logDir, "*.log", 20)
		pruneLogs(logDir, "*_pad.txt", 20)
		stamp := time.Now().Format("2006-01-02_150405")
		var closeCrash func()
		// A GUI build has no stderr, so a Go panic or fatal runtime error would vanish without a
		// trace. Send it to a file next to the log; a clean exit deletes the (empty) file.
		crashPath := filepath.Join(logDir, stamp+"_pad.txt")
		if cf, err := os.Create(crashPath); err == nil {
			_ = debug.SetCrashOutput(cf, debug.CrashOptions{})
			cf.Close()
			closeCrash = func() {
				if st, err := os.Stat(crashPath); err == nil && st.Size() == 0 {
					_ = os.Remove(crashPath)
				}
			}
		}
		if f, err := os.Create(filepath.Join(logDir, stamp+".log")); err == nil {
			writers = append(writers, f)
			closeFn = func() {
				f.Close()
				if closeCrash != nil {
					closeCrash()
				}
			}
		} else if closeCrash != nil {
			closeFn = closeCrash
		}
	}
	return log.New(io.MultiWriter(writers...), "", log.Ltime), buf, closeFn
}

// heartbeat logs the app's memory every 30 s, so a crash without a message can at least be dated
// and a memory leak spotted.
func heartbeat(logger *log.Logger) {
	var m runtime.MemStats
	for range time.Tick(30 * time.Second) {
		runtime.ReadMemStats(&m)
		logger.Printf("běží: paměť %d MB, vláken %d", m.Sys>>20, runtime.NumGoroutine())
	}
}

// pruneLogs keeps the newest `keep` files matching pattern.
func pruneLogs(dir, pattern string, keep int) {
	files, _ := filepath.Glob(filepath.Join(dir, pattern))
	if len(files) <= keep {
		return
	}
	// Names start with a timestamp, so lexical order is chronological.
	for _, f := range files[:len(files)-keep] {
		_ = os.Remove(f)
	}
}
