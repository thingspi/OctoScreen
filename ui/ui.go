package ui

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-systemd/daemon"
	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/gtk"
	"github.com/mcuadros/go-octoprint"
	"github.com/sirupsen/logrus"
)

var (
	StylePath    string
	WindowName   = "OctoScreen"
	WindowHeight = 480
	WindowWidth  = 800
)

const (
	ImageFolder = "images"
	CSSFilename = "style.css"
)

type UI struct {
	Current Panel
	Printer *octoprint.Client
	State   octoprint.ConnectionState
	UIState string

	Notifications *Notifications

	s *SplashPanel
	b *BackgroundTask
	g *gtk.Grid
	o *gtk.Overlay
	w *gtk.Window
	t time.Time

	width, height int
	scaleFactor   int

	sync.Mutex
}

func New(endpoint, key string, width, height int) *UI {
	if width == 0 || height == 0 {
		width = WindowWidth
		height = WindowHeight
	}

	ui := &UI{
		Printer:       octoprint.NewClient(endpoint, key),
		Notifications: NewNotifications(),

		w: MustWindow(gtk.WINDOW_TOPLEVEL),
		t: time.Now(),

		width:  width,
		height: height,
	}

	switch {
	case width > 480:
		ui.scaleFactor = 2
	case width > 1000:
		ui.scaleFactor = 3
	default:
		ui.scaleFactor = 1
	}

	ui.s = NewSplashPanel(ui)
	ui.b = NewBackgroundTask(time.Second*5, ui.verifyConnection)
	ui.initialize()
	return ui
}

func (ui *UI) initialize() {
	defer ui.w.ShowAll()
	ui.loadStyle()

	ui.w.SetTitle(WindowName)
	ui.w.SetDefaultSize(ui.width, ui.height)
	ui.w.SetResizable(false)

	ui.w.Connect("show", ui.b.Start)
	ui.w.Connect("destroy", func() {
		gtk.MainQuit()
	})

	ui.o = MustOverlay()
	ui.w.Add(ui.o)

	ui.g = MustGrid()
	ui.o.Add(ui.g)
	ui.o.AddOverlay(ui.Notifications)

	ui.sdNotify("READY=1")
}

func (ui *UI) loadStyle() {
	p := MustCSSProviderFromFile(CSSFilename)

	s, err := gdk.ScreenGetDefault()
	if err != nil {
		logrus.Errorf("Error getting GDK screen: %s", err)
		return
	}

	gtk.AddProviderForScreen(s, p, gtk.STYLE_PROVIDER_PRIORITY_USER)
}

var errMercyPeriod = time.Second * 30

func (ui *UI) verifyConnection() {

	ui.sdNotify("WATCHDOG=1")

	newUiState := "splash"

	s, err := (&octoprint.ConnectionRequest{}).Do(ui.Printer)
	if err == nil {
		ui.State = s.Current.State
		switch {
		case s.Current.State.IsOperational():
			newUiState = "idle"
		case s.Current.State.IsPrinting():
			newUiState = "printing"
		case s.Current.State.IsError():
			fallthrough
		case s.Current.State.IsOffline():
			if err := (&octoprint.ConnectRequest{}).Do(ui.Printer); err != nil {
				newUiState = "splash"
				ui.s.Label.SetText(fmt.Sprintf("Error connecting to printer: %s", err))
			}
		case s.Current.State.IsConnecting():
			ui.s.Label.SetText(string(s.Current.State))
		}
	} else {
		if time.Since(ui.t) > errMercyPeriod {
			ui.s.Label.SetText(ui.errToUser(err))
		}

		newUiState = "splash"
		Logger.Debugf("Unexpected error: %s", err)
	}

	defer func() { ui.UIState = newUiState }()

	if newUiState == ui.UIState {
		return
	}

	switch newUiState {
	case "idle":
		Logger.Info("Printer is ready")
		ui.Add(IdleStatusPanel(ui))
	case "printing":
		Logger.Info("Printing a job")
		ui.Add(PrintStatusPanel(ui))
	case "splash":
		ui.Add(ui.s)
	}
}

func (ui *UI) sdNotify(m string) {
	_, err := daemon.SdNotify(false, m)

	if err != nil {
		logrus.Errorf("Error sending notification: %s", err)
		return
	}
}

func (ui *UI) Add(p Panel) {
	if ui.Current != nil {
		ui.Remove(ui.Current)
	}

	ui.Current = p
	ui.Current.Show()
	ui.g.Attach(ui.Current.Grid(), 1, 0, 1, 1)
	ui.g.ShowAll()
}

func (ui *UI) Remove(p Panel) {
	defer p.Hide()
	ui.g.Remove(p.Grid())
}

func (ui *UI) GoHistory() {
	ui.Add(ui.Current.Parent())
}

func (ui *UI) errToUser(err error) string {
	text := err.Error()
	if strings.Contains(text, "connection refused") {
		return fmt.Sprintf(
			"Unable to connect to %q (Key: %v), \nmaybe OctoPrint not running?",
			ui.Printer.Endpoint, ui.Printer.APIKey != "",
		)
	}

	return fmt.Sprintf("Unexpected error: %s", err)
}
