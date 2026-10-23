package ui

import (
	"context"
	"fmt"
	"image/color"
	"strings"
	"time"

	"aircast/internal/config"
	"aircast/internal/diagnostics"
	"aircast/internal/discovery"
	"aircast/internal/receiver"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const probeIdleText = "Checks reachability only. Does not start sharing."

type Panel struct {
	app              fyne.App
	window           fyne.Window
	inventory        *receiver.Inventory
	configPath       string
	configErr        error
	devices          []receiver.Device
	selectedID       string
	selectedEndpoint string
	list             *widget.List
	status           *widget.Label
	empty            *fyne.Container
	details          *fyne.Container
	refreshButton    *widget.Button
	scanning         bool
	probing          bool
	probeCancel      context.CancelFunc
	probeID          uint64
	probeText        string
	ctx              context.Context
	cancel           context.CancelFunc
	trayAvailable    bool
	footer           *widget.Label
	quit             func()
	quitDialog       *dialog.ConfirmDialog
	quitting         bool
	cast             castingPanel
}

func New(app fyne.App, path string) *Panel {
	app.Settings().SetTheme(panelTheme{})
	app.SetIcon(Icon)
	saved, configErr := config.Load(path)
	ctx, cancel := context.WithCancel(context.Background())
	p := &Panel{app: app, configPath: path, configErr: configErr, inventory: receiver.NewInventory(saved), ctx: ctx, cancel: cancel, trayAvailable: hasTrayHost(), quit: app.Quit}
	if _, ok := app.(desktop.App); !ok {
		p.trayAvailable = false
	}
	p.window = app.NewWindow("Aircast")
	p.window.SetIcon(Icon)
	p.window.Resize(fyne.NewSize(548, 820))
	p.build()
	p.render()
	return p
}

func (p *Panel) Run(ctx context.Context, quit func()) {
	p.cast.mu.Lock()
	if p.cast.closed.Load() {
		p.cast.mu.Unlock()
		return
	}
	p.cancel()
	p.ctx, p.cancel = context.WithCancel(ctx)
	p.cast.mu.Unlock()
	if quit != nil {
		p.quit = quit
	}
	p.startSender()
	if tray, ok := p.app.(desktop.App); ok {
		tray.SetSystemTrayIcon(Icon)
		quitItem := fyne.NewMenuItem("Quit Aircast", func() { fyne.Do(p.requestQuit) })
		quitItem.IsQuit = true
		tray.SetSystemTrayMenu(fyne.NewMenu("Aircast",
			fyne.NewMenuItem("Show Aircast", func() { fyne.Do(p.window.Show) }),
			fyne.NewMenuItem("Refresh receivers", func() { fyne.Do(p.scan) }),
			fyne.NewMenuItemSeparator(),
			quitItem,
		))
		if p.trayAvailable {
			tray.SetSystemTrayWindow(p.window)
		}
	}
	p.window.SetCloseIntercept(p.closeWindow)
	p.window.Show()
	if p.configErr != nil {
		dialog.ShowError(fmt.Errorf("saved receivers could not be loaded; saving is disabled to protect the existing file: %w", p.configErr), p.window)
	}
	p.scan()
	ctx = p.ctx
	go func() {
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				p.Close()
				return
			case <-ticker.C:
				fyne.Do(func() {
					if ctx.Err() == nil && !p.cast.closed.Load() {
						p.scan()
					}
				})
			}
		}
	}()
}

func (p *Panel) build() {
	mark := canvas.NewImageFromResource(Icon)
	mark.SetMinSize(fyne.NewSize(46, 46))
	mark.FillMode = canvas.ImageFillContain
	title := canvas.NewText("Aircast", ink)
	title.TextSize, title.TextStyle = 28, fyne.TextStyle{Bold: true}
	caption := canvas.NewText("Your screen, on the big screen.", slate)
	caption.TextSize = 13
	about := widget.NewButtonWithIcon("", theme.InfoIcon(), p.about)
	about.Importance = widget.LowImportance
	header := container.NewBorder(nil, nil, mark, about, container.NewVBox(title, caption))
	p.status = widget.NewLabel("Ready to find receivers")
	p.status.Wrapping = fyne.TextWrapWord
	p.status.Importance = widget.LowImportance
	p.refreshButton = widget.NewButtonWithIcon("Refresh", theme.ViewRefreshIcon(), p.scan)
	add := widget.NewButtonWithIcon("Add", theme.ContentAddIcon(), p.add)
	section := widget.NewLabelWithStyle("Receivers", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	section.SizeName = theme.SizeNameSubHeadingText
	toolbar := container.NewBorder(nil, nil, section, container.NewHBox(p.refreshButton, add), nil)
	p.list = widget.NewList(func() int { return len(p.devices) }, func() fyne.CanvasObject {
		icon := widget.NewIcon(theme.ComputerIcon())
		name := widget.NewLabelWithStyle("Receiver", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		name.Truncation = fyne.TextTruncateEllipsis
		address := widget.NewLabel("192.168.1.100:7000")
		address.Importance = widget.LowImportance
		address.Truncation = fyne.TextTruncateEllipsis
		kind := widget.NewLabel("AirPlay")
		kind.Importance = widget.LowImportance
		return container.NewBorder(nil, nil, icon, kind, container.NewVBox(name, address))
	}, func(id widget.ListItemID, object fyne.CanvasObject) {
		d := p.devices[id]
		row := object.(*fyne.Container)
		text := row.Objects[0].(*fyne.Container)
		text.Objects[0].(*widget.Label).SetText(d.Name)
		state := "Nearby"
		if !d.Online {
			state = "Not seen on this network"
		}
		if d.Saved {
			state = "Saved / " + state
		}
		text.Objects[1].(*widget.Label).SetText(state)
		row.Objects[2].(*widget.Label).SetText(d.Kind.Label())
		icon := theme.ComputerIcon()
		if d.Kind == receiver.Audio {
			icon = theme.VolumeUpIcon()
		}
		row.Objects[1].(*widget.Icon).SetResource(icon)
	})
	p.list.OnSelected = func(id widget.ListItemID) {
		if id < 0 || id >= len(p.devices) {
			return
		}
		p.selectDevice(p.devices[id].ID)
		p.renderDetails()
		p.renderCasting()
	}
	emptyTitle := widget.NewLabelWithStyle("No receivers found yet", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	emptyHelp := widget.NewLabel("Connect this computer and your TV to the same network.\nIf discovery is blocked, add the receiver's IP address.")
	emptyHelp.Alignment, emptyHelp.Wrapping = fyne.TextAlignCenter, fyne.TextWrapWord
	p.empty = container.NewCenter(container.NewVBox(widget.NewIcon(theme.ComputerIcon()), emptyTitle, emptyHelp))
	listBackground := canvas.NewRectangle(color.White)
	listBackground.CornerRadius = 8
	receivers := container.NewStack(listBackground, p.list, p.empty)
	p.details = container.NewVBox()
	p.footer = widget.NewLabel("Closing this window keeps Aircast in the tray.")
	if !p.trayAvailable {
		p.footer.SetText("No tray host detected. Closing this window quits Aircast.")
	}
	p.footer.Importance, p.footer.Wrapping = widget.LowImportance, fyne.TextWrapWord
	p.footer.SizeName = theme.SizeNameCaptionText
	quitButton := widget.NewButtonWithIcon("Quit", theme.LogoutIcon(), p.requestQuit)
	quitButton.Importance = widget.LowImportance
	p.buildCasting()
	bottom := container.NewVBox(widget.NewSeparator(), p.details, widget.NewSeparator(), p.cast.box,
		container.NewBorder(nil, nil, nil, quitButton, p.footer))
	content := container.NewBorder(container.NewVBox(header, widget.NewSeparator(), toolbar, p.status), bottom, nil, nil, receivers)
	p.window.SetContent(container.New(layout.NewCustomPaddedLayout(20, 16, 20, 20), content))
}

func (p *Panel) render() {
	p.selectDevice(p.inventory.ResolveID(p.selectedID))
	p.devices = p.inventory.Snapshot(time.Now())
	if len(p.devices) == 0 {
		p.empty.Show()
	} else {
		p.empty.Hide()
	}
	p.list.Refresh()
	index := -1
	for n, d := range p.devices {
		if d.ID == p.selectedID {
			index = n
			break
		}
	}
	if index >= 0 {
		p.list.Select(index)
	} else {
		p.list.UnselectAll()
		p.selectDevice("")
	}
	p.renderDetails()
	p.renderCasting()
}

func (p *Panel) selectDevice(id string) {
	if p.selectedID == id {
		return
	}
	if request := p.cast.pendingSwitch; request != nil && p.inventory.ResolveID(id) != p.inventory.ResolveID(request.device.ID) {
		p.cancelSwitch()
	}
	p.cancelProbe()
	p.probeText = probeIdleText
	p.selectedID = id
}

func (p *Panel) selected() (receiver.Device, bool) {
	for _, d := range p.devices {
		if d.ID == p.selectedID {
			return d, true
		}
	}
	return receiver.Device{}, false
}

func (p *Panel) renderDetails() {
	d, ok := p.selected()
	endpoint := ""
	if ok {
		endpoint = d.Endpoint()
	}
	if p.selectedEndpoint != endpoint {
		p.cancelProbe()
		p.probeText = probeIdleText
		p.selectedEndpoint = endpoint
	}
	if !ok {
		help := widget.NewLabel("Select a receiver to see its address and check its response.")
		help.Wrapping, help.Importance = fyne.TextWrapWord, widget.LowImportance
		p.details.Objects = []fyne.CanvasObject{help}
		p.details.Refresh()
		return
	}
	name := widget.NewLabelWithStyle(d.Name, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	name.Truncation = fyne.TextTruncateEllipsis
	name.SizeName = theme.SizeNameSubHeadingText
	model := d.Kind.Label()
	if d.Model != "" {
		model += " / " + d.Model
	}
	subtitle := widget.NewLabel(model)
	subtitle.Truncation, subtitle.Importance = fyne.TextTruncateEllipsis, widget.LowImportance
	subtitle.SizeName = theme.SizeNameCaptionText
	address := widget.NewLabel(d.Endpoint())
	address.Wrapping = fyne.TextWrapBreak
	address.Selectable = true
	check := widget.NewButtonWithIcon("Check receiver", theme.SearchIcon(), p.probe)
	check.Importance = widget.LowImportance
	if p.probing {
		check.SetText("Checking...")
		check.Disable()
	}
	saveText := "Save receiver"
	if d.Saved {
		saveText = "Remove saved"
	}
	save := widget.NewButtonWithIcon(saveText, theme.DocumentSaveIcon(), func() { p.toggleSaved(d) })
	if p.configErr != nil {
		save.Disable()
	}
	result := widget.NewLabel(p.probeText)
	result.Wrapping, result.Importance = fyne.TextWrapWord, widget.LowImportance
	if p.probeText == "" {
		result.SetText(probeIdleText)
	}
	p.details.Objects = []fyne.CanvasObject{
		name, container.NewBorder(nil, nil, nil, container.NewHBox(check, save), container.NewVBox(subtitle, address)), result,
	}
	p.details.Refresh()
}

func (p *Panel) scan() {
	if p.scanning || p.ctx.Err() != nil || p.cast.closed.Load() {
		return
	}
	p.scanning = true
	p.refreshButton.Disable()
	p.status.SetText("Searching your local network...")
	ctx := p.ctx
	go func() {
		err := discovery.Scan(ctx, 4*time.Second, func(d receiver.Device) {
			seen := time.Now()
			fyne.Do(func() {
				if ctx.Err() == nil && !p.cast.closed.Load() {
					p.inventory.Observe(d, seen)
				}
			})
		})
		if ctx.Err() != nil {
			return
		}
		fyne.Do(func() {
			if ctx.Err() != nil || p.cast.closed.Load() {
				return
			}
			p.scanning = false
			p.refreshButton.Enable()
			p.render()
			nearby := 0
			for _, d := range p.devices {
				if d.Online {
					nearby++
				}
			}
			if err != nil {
				p.status.SetText("Discovery incomplete. " + err.Error() + ". You can add an IP address.")
			} else {
				p.status.SetText(fmt.Sprintf("%d nearby / %d saved. Checked %s", nearby, len(p.inventory.Saved()), time.Now().Format("15:04")))
			}
		})
	}()
}

func (p *Panel) add() {
	name, address := widget.NewEntry(), widget.NewEntry()
	name.SetPlaceHolder("Living room TV")
	address.SetPlaceHolder("192.168.1.40 or tv.local:7000")
	kind := widget.NewSelect([]string{receiver.AirPlay.Label(), receiver.Audio.Label(), receiver.Cast.Label()}, nil)
	kind.SetSelected(receiver.AirPlay.Label())
	form := dialog.NewForm("Add a receiver", "Save receiver", "Cancel", []*widget.FormItem{
		widget.NewFormItem("Name", name), widget.NewFormItem("Address", address), widget.NewFormItem("Type", kind),
	}, func(ok bool) {
		if !ok {
			return
		}
		if p.configErr != nil {
			dialog.ShowError(fmt.Errorf("saving is disabled until the receiver configuration is repaired: %w", p.configErr), p.window)
			return
		}
		protocol := receiver.AirPlay
		if kind.Selected == receiver.Audio.Label() {
			protocol = receiver.Audio
		} else if kind.Selected == receiver.Cast.Label() {
			protocol = receiver.Cast
		}
		d, err := receiver.Manual(name.Text, address.Text, protocol)
		if err != nil {
			dialog.ShowError(err, p.window)
			return
		}
		p.saveDevice(d)
	}, p.window)
	form.Resize(fyne.NewSize(460, 240))
	form.Show()
}

func (p *Panel) saveDevice(d receiver.Device) {
	before := p.inventory.Saved()
	if err := p.inventory.Save(d); err != nil {
		dialog.ShowError(err, p.window)
		return
	}
	if err := config.Save(p.configPath, p.inventory.Saved()); err != nil {
		p.rollbackSaved(before)
		dialog.ShowError(err, p.window)
		return
	}
	p.selectDevice(p.inventory.ResolveID(d.ID))
	p.render()
}

func (p *Panel) toggleSaved(d receiver.Device) {
	if !d.Saved {
		p.saveDevice(d)
		return
	}
	dialog.ShowConfirm("Remove saved receiver?", "This removes the saved address, not a pairing or anything on your TV.", func(ok bool) {
		if !ok {
			return
		}
		before := p.inventory.Saved()
		p.inventory.Forget(d.ID)
		if err := config.Save(p.configPath, p.inventory.Saved()); err != nil {
			p.rollbackSaved(before)
			dialog.ShowError(err, p.window)
		}
		p.render()
	}, p.window)
}

func (p *Panel) rollbackSaved(saved []receiver.Device) {
	for _, d := range p.inventory.Saved() {
		p.inventory.Forget(d.ID)
	}
	for _, d := range saved {
		p.inventory.Save(d)
	}
}

func (p *Panel) cancelProbe() {
	if p.probeCancel != nil {
		p.probeCancel()
		p.probeCancel = nil
	}
	p.probeID++
	p.probing = false
}

func (p *Panel) probe() {
	d, ok := p.selected()
	if !ok || p.probing || p.quitting || p.ctx.Err() != nil || p.cast.closed.Load() {
		return
	}
	p.cancelProbe()
	ctx, cancel := context.WithCancel(p.ctx)
	p.probeCancel = cancel
	p.probing, p.probeText = true, "Checking receiver..."
	requestID := p.probeID
	p.renderDetails()
	go func() {
		result, err := diagnostics.Probe(ctx, d)
		if ctx.Err() != nil {
			cancel()
			return
		}
		fyne.Do(func() {
			defer cancel()
			if ctx.Err() != nil {
				return
			}
			p.completeProbe(requestID, d.ID, d.Endpoint(), result, err)
		})
	}()
}

func (p *Panel) completeProbe(requestID uint64, deviceID, endpoint string, result diagnostics.Result, err error) {
	if p.cast.closed.Load() || p.ctx.Err() != nil || requestID != p.probeID || p.selectedID != deviceID {
		return
	}
	if device, ok := p.selected(); !ok || device.Endpoint() != endpoint {
		return
	}
	p.probing = false
	p.probeCancel = nil
	if err != nil {
		p.probeText = err.Error()
	} else {
		p.probeText = result.Summary + ". " + result.Detail
		if result.Model != "" {
			p.probeText = result.Summary + " / " + result.Model + ". " + result.Detail
		}
	}
	p.renderDetails()
}

func (p *Panel) about() {
	text := strings.Join([]string{
		"Aircast 0.1.0",
		"Go + native C/CGO desktop application for Windows and Linux.",
		"AirPlay screen sharing uses a separate local sender. Google Cast and audio-only receiver streaming are not supported yet.",
		"Best available prioritizes detail at the receiver's supported resolution. System audio is beta and off by default.",
		"No account, telemetry, remote-control server, or automatic screen capture.",
		"A receiver check tests reachability, not sharing. Sender statistics do not verify the picture on your TV.",
		"Close the window to keep sharing in the tray only when a tray host is available. Quit stops the sender before exiting.",
		"On GNOME, a StatusNotifier/AppIndicator extension is needed for a tray icon.",
	}, "\n\n")
	label := widget.NewLabel(text)
	label.Wrapping = fyne.TextWrapWord
	d := dialog.NewCustom("About Aircast", "Close", label, p.window)
	d.Resize(fyne.NewSize(460, 500))
	d.Show()
}
