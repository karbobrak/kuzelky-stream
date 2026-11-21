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
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"kuzelkystream/internal/app"
	"kuzelkystream/internal/config"
	"kuzelkystream/internal/stream"
	"kuzelkystream/internal/tray"
	"kuzelkystream/internal/web"
	"kuzelkystream/internal/win"
)

// version is set at build time: -ldflags "-X main.version=1.0.0" (see build.ps1).
var version = "vývojová"

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
	flag.Parse()
	portFile := filepath.Join(dir, "data", "port.txt")

	if _, err := win.SingleInstance("KuzelkyStream"); err != nil {
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
	cloudCtx, stopCloud := context.WithCancel(context.Background())
	go a.RunCloud(cloudCtx)

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
	go func() {
		if err := web.Serve(ln, port, a); err != nil {
			logger.Printf("webový server: %v", err)
		}
	}()
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
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0o755); err == nil {
		pruneLogs(filepath.Join(dir, "logs"), 20)
		if f, err := os.Create(filepath.Join(dir, "logs", time.Now().Format("2006-01-02_150405")+".log")); err == nil {
			writers = append(writers, f)
			closeFn = func() { f.Close() }
		}
	}
	return log.New(io.MultiWriter(writers...), "", log.Ltime), buf, closeFn
}

// pruneLogs keeps the newest `keep` .log files.
func pruneLogs(dir string, keep int) {
	files, _ := filepath.Glob(filepath.Join(dir, "*.log"))
	if len(files) <= keep {
		return
	}
	// Names start with a timestamp, so lexical order is chronological.
	for _, f := range files[:len(files)-keep] {
		_ = os.Remove(f)
	}
}
