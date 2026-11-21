package tray

import (
	"time"

	"fyne.io/systray"
)

// State selects the icon and tooltip.
type State int

const (
	Idle State = iota
	Live
	Warn
)

// Options for Run.
type Options struct {
	OnOpen func()
	// OnQuit is called from the menu; return false to cancel quitting.
	OnQuit func() bool
	// Status is polled every 2 s to update the icon and tooltip.
	Status func() (State, string)
	// OnReady runs once the icon is shown (start the rest of the app here).
	OnReady func()
}

// Run shows the icon and blocks until Quit. Must be called from main.
func Run(o Options) {
	icons := map[State][]byte{Idle: makeIcon(colIdle), Live: makeIcon(colLive), Warn: makeIcon(colWarn)}
	systray.Run(func() {
		systray.SetIcon(icons[Idle])
		systray.SetTitle("Kuželky Stream")
		systray.SetTooltip("Kuželky Stream")
		mOpen := systray.AddMenuItem("Otevřít ovládání", "Otevře ovládání v prohlížeči")
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("Ukončit", "Ukončí vysílání i aplikaci")
		systray.SetOnTapped(o.OnOpen) // left click opens the UI; right click shows the menu

		go func() {
			for {
				select {
				case <-mOpen.ClickedCh:
					o.OnOpen()
				case <-mQuit.ClickedCh:
					if o.OnQuit() {
						systray.Quit()
						return
					}
				}
			}
		}()
		go func() {
			last := State(-1)
			for {
				st, tip := o.Status()
				if st != last {
					systray.SetIcon(icons[st])
					last = st
				}
				systray.SetTooltip(tip)
				time.Sleep(2 * time.Second)
			}
		}()
		if o.OnReady != nil {
			o.OnReady()
		}
	}, func() {})
}

// Quit removes the icon and makes Run return.
func Quit() { systray.Quit() }
