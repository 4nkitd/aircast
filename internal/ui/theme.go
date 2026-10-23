package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

var ink = color.NRGBA{R: 0x16, G: 0x32, B: 0x46, A: 255}
var slate = color.NRGBA{R: 0x61, G: 0x75, B: 0x86, A: 255}
var harbor = color.NRGBA{R: 0x24, G: 0x5f, B: 0x88, A: 255}

type panelTheme struct{}

func (panelTheme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameBackground:
		return color.NRGBA{R: 0xf2, G: 0xf5, B: 0xf7, A: 255}
	case theme.ColorNameForeground:
		return ink
	case theme.ColorNamePrimary, theme.ColorNameHyperlink:
		return harbor
	case theme.ColorNameDisabled, theme.ColorNamePlaceHolder:
		return slate
	case theme.ColorNameInputBackground, theme.ColorNameOverlayBackground:
		return color.White
	case theme.ColorNameButton:
		return color.NRGBA{R: 0xe4, G: 0xeb, B: 0xef, A: 255}
	case theme.ColorNameSelection:
		return color.NRGBA{R: 0xd7, G: 0xe8, B: 0xf4, A: 255}
	case theme.ColorNameSuccess:
		return color.NRGBA{R: 0x23, G: 0x7b, B: 0x67, A: 255}
	}
	return theme.DefaultTheme().Color(name, theme.VariantLight)
}

func (panelTheme) Font(style fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(style)
}

func (panelTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func (panelTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNameText:
		return 14
	case theme.SizeNameCaptionText:
		return 12
	case theme.SizeNamePadding:
		return 8
	case theme.SizeNameInnerPadding:
		return 10
	case theme.SizeNameInputRadius, theme.SizeNameButtonRadius:
		return 6
	}
	return theme.DefaultTheme().Size(name)
}

var Icon = fyne.NewStaticResource("aircast.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64" viewBox="0 0 64 64"><rect x="3" y="3" width="58" height="58" rx="15" fill="#163246"/><path d="M17 40V18h30v24H34" fill="none" stroke="#fff" stroke-width="3.5" stroke-linejoin="round"/><path d="M17 33c8 0 14 6 14 14M17 40c4 0 7 3 7 7" fill="none" stroke="#8dcbe7" stroke-width="3.5"/><circle cx="17" cy="47" r="2.2" fill="#8dcbe7"/></svg>`))
