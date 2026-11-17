package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// sttCommand bekommt den WAV-Pfad als $1 und schreibt den Text nach stdout.
// Liegt als Skript in ~/.local/bin und laeuft rein lokal -- siehe README.
const sttCommand = "rc-tui-stt"

type sttMsg struct {
	text string
	err  error
}

// startRecording spawns arecord into a temp file. 16 kHz mono ist das, was
// Whisper ohnehin intern erwartet -- alles andere waere Resampling umsonst.
func (m *model) startRecording() {
	f, err := os.CreateTemp("", "rc-tui-*.wav")
	if err != nil {
		m.fail(err)
		return
	}
	f.Close()
	dev := os.Getenv("RC_TUI_AUDIO_DEV")
	if dev == "" {
		dev = "default" // folgt dem Standard-Eingang von PipeWire
	}
	cmd := exec.Command("arecord", "-q", "-D", dev,
		"-f", "S16_LE", "-r", "16000", "-c", "1", f.Name())
	if err := cmd.Start(); err != nil {
		os.Remove(f.Name())
		m.fail(fmt.Errorf("arecord: %w", err))
		return
	}
	m.rec, m.recPath = cmd, f.Name()
	m.dictStatus = "🎤 Aufnahme läuft · ctrl+d beendet"
}

// stopRecording ends the recording and hands the file to the helper.
func (m *model) stopRecording() tea.Cmd {
	cmd, path := m.rec, m.recPath
	m.rec, m.recPath = nil, ""
	if cmd == nil {
		return nil
	}
	cmd.Process.Signal(os.Interrupt)
	cmd.Wait()
	m.dictStatus = "⏳ transkribiere…"
	return func() tea.Msg {
		defer os.Remove(path)
		out, err := exec.Command(sttCommand, path).Output()
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
				return sttMsg{err: fmt.Errorf("%s: %s", sttCommand, strings.TrimSpace(string(ee.Stderr)))}
			}
			return sttMsg{err: fmt.Errorf("%s: %w", sttCommand, err)}
		}
		return sttMsg{text: strings.TrimSpace(string(out))}
	}
}

// killRecording drops a running recording without transcribing it.
func (m *model) killRecording() {
	if m.rec == nil {
		return
	}
	m.rec.Process.Kill()
	m.rec.Wait()
	os.Remove(m.recPath)
	m.rec, m.recPath, m.dictStatus = nil, "", ""
}

// insertDictation puts the transcript at the caret and opens the input field.
func (m *model) insertDictation(text string) tea.Cmd {
	var cmd tea.Cmd
	if m.focus != focusCompose {
		m.focus = focusCompose
		m.sugg, m.scurs = nil, 0
		cmd = m.ta.Focus()
	}
	left, right, rows, row := m.leftOfCursor()
	if left != "" && !strings.HasSuffix(left, " ") {
		left += " "
	}
	rows[row] = left + text + right
	m.ta.SetValue(strings.Join(rows, "\n"))
	m.moveCursorTo(row, len([]rune(left+text)))
	m.growCompose()
	m.layout()
	m.renderOpen()
	return cmd
}
