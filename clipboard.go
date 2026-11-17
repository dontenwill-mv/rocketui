package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// clipboardTools in der Reihenfolge, in der wir sie probieren. Alle drei
// forken sich weg, sobald sie den Text gelesen haben -- Run() blockiert nicht.
var clipboardTools = [][]string{
	{"wl-copy"},
	{"xclip", "-selection", "clipboard"},
	{"xsel", "--clipboard", "--input"},
}

// copyFn ist die Indirektion für Tests: der Tastendruck ruft nie direkt das
// echte Clipboard, sonst räumt jeder Testlauf die Ablage des Users ab.
var copyFn = copyToClipboard

// copyToClipboard legt s in die Zwischenablage: erst über ein lokales Tool,
// sonst per OSC 52. Letzteres reicht tmux (set-clipboard on/external) nach
// außen durch und funktioniert damit auch über SSH.
func copyToClipboard(s string) error {
	for _, t := range clipboardTools {
		if _, err := exec.LookPath(t[0]); err != nil {
			continue
		}
		cmd := exec.Command(t[0], t[1:]...)
		cmd.Stdin = strings.NewReader(s)
		if err := cmd.Run(); err == nil {
			return nil
		}
	}
	return osc52(s)
}

func osc52(s string) error {
	enc := base64.StdEncoding.EncodeToString([]byte(s))
	_, err := fmt.Fprintf(os.Stdout, "\x1b]52;c;%s\a", enc)
	return err
}

// codeBlocks liefert den Inhalt aller ```-Blöcke einer Nachricht, ohne Zäune
// und ohne Sprachangabe. Ein nicht geschlossener Block zählt mit -- angezeigt
// wird er auch.
func codeBlocks(text string) []string {
	var out []string
	var cur []string
	in := false
	for _, ln := range strings.Split(text, "\n") {
		if reFence.MatchString(ln) {
			if in {
				out = append(out, strings.Join(cur, "\n"))
				cur, in = nil, false
			} else {
				in = true
			}
			continue
		}
		if in {
			cur = append(cur, ln)
		}
	}
	if in && len(cur) > 0 {
		out = append(out, strings.Join(cur, "\n"))
	}
	return out
}
