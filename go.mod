module github.com/linuxmatters/jivetalking

go 1.26.3

require (
	charm.land/bubbletea/v2 v2.0.7
	charm.land/lipgloss/v2 v2.0.3
	github.com/alecthomas/kong v1.15.0
	github.com/linuxmatters/ffmpeg-statigo v0.0.0-00010101000000-000000000000
)

replace github.com/linuxmatters/ffmpeg-statigo => ./third_party/ffmpeg-statigo

require (
	github.com/charmbracelet/colorprofile v0.4.3 // indirect
	github.com/charmbracelet/ultraviolet v0.0.0-20260525132238-948f4557a654 // indirect
	github.com/charmbracelet/x/ansi v0.11.7 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.0 // indirect
	github.com/mattn/go-runewidth v0.0.23 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
)
