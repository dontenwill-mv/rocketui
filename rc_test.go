package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
)

func TestRCTimeBothFormats(t *testing.T) {
	var a struct {
		TS rcTime `json:"ts"`
	}
	if err := json.Unmarshal([]byte(`{"ts":"2026-08-24T07:30:00.000Z"}`), &a); err != nil {
		t.Fatal(err)
	}
	if a.TS.Hour() != 7 || a.TS.Minute() != 30 {
		t.Fatalf("ISO falsch geparst: %v", a.TS)
	}
	var b struct {
		TS rcTime `json:"ts"`
	}
	if err := json.Unmarshal([]byte(`{"ts":{"$date":1756020600000}}`), &b); err != nil {
		t.Fatal(err)
	}
	if b.TS.IsZero() {
		t.Fatal("$date nicht geparst")
	}
}

func TestDDPEvents(t *testing.T) {
	raw := []byte(`{"msg":"changed","collection":"stream-room-messages","fields":{
		"eventName":"__my_messages__",
		"args":[{"_id":"m1","rid":"r1","msg":"hallo","ts":{"$date":1756020600000},
		         "u":{"_id":"u1","username":"max","name":"Max"}}]}}`)
	var f ddpFrame
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	ev := f.events()
	if len(ev) != 1 {
		t.Fatalf("erwartet 1 event, bekam %d", len(ev))
	}
	nm, ok := ev[0].(newMessageMsg)
	if !ok || nm.m.Msg != "hallo" || nm.m.Author() != "Max" {
		t.Fatalf("falsches event: %#v", ev[0])
	}

	// subscriptions-changed: args[0] ist ein String und muss übersprungen werden.
	raw = []byte(`{"msg":"changed","collection":"stream-notify-user","fields":{
		"eventName":"u1/subscriptions-changed",
		"args":["updated",{"rid":"r2","name":"team","t":"c","unread":3,"open":true}]}}`)
	f = ddpFrame{}
	json.Unmarshal(raw, &f)
	ev = f.events()
	if len(ev) != 1 {
		t.Fatalf("erwartet 1 event, bekam %d", len(ev))
	}
	if sc := ev[0].(subChangedMsg); sc.r.ID != "r2" || sc.r.Unread != 3 {
		t.Fatalf("falsches sub-event: %#v", sc.r)
	}
}

func TestSearchRebuild(t *testing.T) {
	m := model{
		rooms: []Room{
			{ID: "1", Name: "entwicklung", FName: "Entwicklung", Type: "c"},
			{ID: "2", Name: "max.mueller", FName: "Max Müller", Type: "d"},
			{ID: "3", Name: "urlaub", FName: "Urlaub", Type: "p"},
		},
		slUsers: []SpotlightUser{{Username: "max.mueller"}, {Username: "maxine", Name: "Maxine Neu"}},
		ti:      textinput.New(),
	}
	m.ti.SetValue("max")
	m.rebuild()

	if len(m.results) == 0 {
		t.Fatal("keine Ergebnisse")
	}
	if m.results[0].kind != resHeader {
		t.Fatalf("erste Zeile muss Header sein, ist %v", m.results[0].kind)
	}
	if m.results[m.rcursor].kind == resHeader {
		t.Fatal("Cursor steht auf einem Header")
	}
	// Fuzzy trifft den DM, nicht 'urlaub'.
	var rooms []string
	users := 0
	for _, r := range m.results {
		switch r.kind {
		case resRoom:
			rooms = append(rooms, r.room.ID)
		case resUser:
			users++
			if r.user.Username == "max.mueller" {
				t.Fatal("bereits abonnierter User darf nicht als Server-Treffer erscheinen")
			}
		}
	}
	if len(rooms) != 1 || rooms[0] != "2" {
		t.Fatalf("erwartet nur Raum 2, bekam %v", rooms)
	}
	if users != 1 {
		t.Fatalf("erwartet 1 Server-User, bekam %d", users)
	}
}

func msg(id, user, text string) Message {
	m := Message{ID: id, RID: "r1", Msg: text}
	m.U.Username = user
	m.U.Name = user
	m.TS.Time = time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	return m
}

func TestRenderCursorAndStarts(t *testing.T) {
	msgs := []Message{msg("a", "max", "erste"), msg("b", "eva", "zweite"), msg("c", "eva", "dritte")}
	msgs[2].TMID = "a" // Thread-Antwort: darf im Raum nicht auftauchen

	r := renderMessages(msgs, renderOpts{width: 40, cursor: 1, me: "max"})
	if len(r.msgs) != 2 {
		t.Fatalf("Thread-Antwort nicht gefiltert: %d Nachrichten", len(r.msgs))
	}
	if len(r.starts) != len(r.msgs) {
		t.Fatalf("starts (%d) passt nicht zu msgs (%d)", len(r.starts), len(r.msgs))
	}
	lines := strings.Split(r.content, "\n")
	if r.starts[1] >= len(lines) {
		t.Fatalf("start-Offset %d liegt außerhalb von %d Zeilen", r.starts[1], len(lines))
	}
	// Nur der Block der Cursor-Nachricht trägt den Marker.
	marked := 0
	for i, l := range lines {
		if strings.Contains(stripStyle(l), "▎") {
			marked++
			if i < r.starts[1] {
				t.Fatalf("Marker in Zeile %d liegt vor dem Cursor-Block %d", i, r.starts[1])
			}
		}
	}
	if marked == 0 {
		t.Fatal("Cursor-Block ist nicht markiert")
	}
}

func TestHasReacted(t *testing.T) {
	m := msg("a", "eva", "hi")
	m.Reactions = map[string]Reaction{":thumbsup:": {Usernames: []string{"eva", "max"}}}
	if !hasReacted(m, "thumbsup", "max") {
		t.Fatal("eigene Reaktion nicht erkannt")
	}
	if hasReacted(m, "thumbsup", "otto") {
		t.Fatal("fremde Reaktion falsch als eigene erkannt")
	}
	if hasReacted(m, "heart", "max") {
		t.Fatal("nicht vorhandenes Emoji als reagiert gemeldet")
	}
}

func TestUserStyleStable(t *testing.T) {
	a := userStyle("eva").GetForeground()
	if a != userStyle("eva").GetForeground() {
		t.Fatal("Farbe ist nicht stabil")
	}
	seen := map[string]int{}
	for _, u := range []string{"eva", "max", "otto", "lisa", "bernd", "anna"} {
		seen[fmt.Sprint(userStyle(u).GetForeground())]++
	}
	if len(seen) < 4 {
		t.Fatalf("zu viele Kollisionen bei 6 Usern: %v", seen)
	}
}

func TestThreadList(t *testing.T) {
	parent := msg("p1", "eva", "Frage?")
	parent.Replies = 2
	r1, r2 := msg("a1", "max", "Antwort 1"), msg("a2", "eva", "Antwort 2")
	r1.TMID, r2.TMID = "p1", "p1"
	other := msg("x1", "otto", "unbeteiligt")

	m := model{
		open: &Room{ID: "r1", Type: "c"},
		msgs: map[string][]Message{"r1": {parent, other, r1, r2}},
	}
	m.thread = &parent
	list := m.threadList()
	if len(list) != 3 {
		t.Fatalf("erwartet Parent + 2 Antworten, bekam %d", len(list))
	}
	if list[0].ID != "p1" {
		t.Fatalf("Parent muss zuerst stehen, ist %s", list[0].ID)
	}
	for _, x := range list[1:] {
		if x.TMID != "p1" {
			t.Fatalf("fremde Nachricht im Thread: %s", x.ID)
		}
	}

	// Raumansicht filtert die Antworten weiter aus.
	room := renderMessages(m.msgs["r1"], renderOpts{width: 40, cursor: -1})
	if len(room.msgs) != 2 {
		t.Fatalf("Raumansicht muss 2 Nachrichten zeigen, zeigt %d", len(room.msgs))
	}
}

func TestRenderTextCodeAndEmoji(t *testing.T) {
	in := "hallo :thumbsup: mit `code` drin\n```go\nfmt.Println(1)\n```\ndanach"
	out := stripStyle(renderText(in, 60))

	if !strings.Contains(out, "👍") {
		t.Fatal("Emoji-Shortcode nicht ersetzt")
	}
	if strings.Contains(out, "```") {
		t.Fatal("Fence-Marker steht noch im Output")
	}
	if strings.Contains(out, "`code`") {
		t.Fatal("Inline-Backticks stehen noch im Output")
	}
	if !strings.Contains(out, "code") || !strings.Contains(out, "fmt.Println(1)") {
		t.Fatalf("Code-Inhalt verloren:\n%s", out)
	}
	if !strings.Contains(out, "danach") {
		t.Fatal("Text nach dem Block fehlt")
	}
	// Emoji-Ersetzung darf im Code-Block nicht stattfinden.
	code := stripStyle(renderText("```\n:thumbsup:\n```", 60))
	if strings.Contains(code, "👍") {
		t.Fatal("Emoji im Code-Block ersetzt")
	}
}

func TestUnreadStaysUntilLeavingSection(t *testing.T) {
	m := model{rooms: []Room{{ID: "1", Type: "c", Unread: 2}, {ID: "2", Type: "c"}}}
	m.enterSection(0)
	if len(m.visible()) != 1 {
		t.Fatalf("erwartet 1 ungelesener Raum, bekam %d", len(m.visible()))
	}
	m.rooms[0].Unread = 0 // gelesen, während man in der Section steht
	if got := m.visible(); len(got) != 1 || got[0].ID != "1" {
		t.Fatalf("gelesener Raum verschwindet zu früh: %v", got)
	}
	m.enterSection(1)
	m.enterSection(0)
	if len(m.visible()) != 0 {
		t.Fatal("Raum bleibt nach Verlassen der Section hängen")
	}
}

func TestEmojiAutocomplete(t *testing.T) {
	m := model{ta: textarea.New()}

	m.ta.SetValue("hallo :thumb")
	m.updateSuggestions()
	if len(m.sugg) == 0 {
		t.Fatal("keine Vorschläge für ':thumb'")
	}
	if !strings.HasPrefix(m.sugg[0].Code, "thumb") {
		t.Fatalf("Prefix-Treffer nicht vorn: %s", m.sugg[0].Code)
	}
	want := m.sugg[0].Glyph
	m.acceptEmoji()
	if strings.Contains(m.ta.Value(), ":thumb") {
		t.Fatalf("Fragment nicht ersetzt: %q", m.ta.Value())
	}
	if !strings.Contains(m.ta.Value(), want) {
		t.Fatalf("Emoji nicht eingesetzt: %q", m.ta.Value())
	}
	if !strings.HasPrefix(m.ta.Value(), "hallo ") {
		t.Fatalf("Text vor dem Fragment beschädigt: %q", m.ta.Value())
	}
	if len(m.sugg) != 0 {
		t.Fatal("Vorschläge bleiben nach Übernahme offen")
	}

	// Doppelpunkt mitten im Wort ist kein Shortcode.
	m.ta.SetValue("Uhrzeit:")
	m.updateSuggestions()
	if len(m.sugg) != 0 {
		t.Fatalf("falscher Trigger bei 'Uhrzeit:': %v", m.sugg[0])
	}
}

func TestFooterHeightGrowsWithCompose(t *testing.T) {
	m := newModel(Config{})
	m.w, m.h = 100, 30
	m.layout()
	single := m.vp.Height
	if m.footerH() != 2 {
		t.Fatalf("Footer ohne Eingabe muss 2 Zeilen sein, ist %d", m.footerH())
	}
	m.focus = focusCompose
	m.ta.SetValue("eine\nzweite\ndritte")
	m.growCompose()
	if m.ta.Height() != 3 {
		t.Fatalf("Eingabefeld muss auf 3 Zeilen wachsen, ist %d", m.ta.Height())
	}
	if m.vp.Height >= single {
		t.Fatalf("Nachrichtenbereich muss schrumpfen: vorher %d, jetzt %d", single, m.vp.Height)
	}
	if 2+m.vp.Height+m.footerH() != m.h {
		t.Fatalf("Höhen passen nicht: 2 + %d + %d != %d", m.vp.Height, m.footerH(), m.h)
	}
}

func TestEmojiAutocompleteAtCursor(t *testing.T) {
	m := model{ta: textarea.New()}
	m.ta.SetWidth(80)
	m.ta.SetValue("a :thumbsup b")
	m.ta.SetCursor(11) // direkt hinter 'thumbsup', nicht am Textende

	m.updateSuggestions()
	if len(m.sugg) == 0 || m.sugg[0].Code != "thumbsup" {
		t.Fatalf("Fragment am Cursor nicht erkannt: %v", m.sugg)
	}
	m.acceptEmoji()
	if got := m.ta.Value(); got != "a 👍 b" {
		t.Fatalf("Ersetzung am Cursor falsch: %q", got)
	}
	// Cursor steht direkt hinter dem Emoji, das vorhandene Leerzeichen bleibt stehen.
	left, right, _, _ := m.leftOfCursor()
	if left != "a 👍" || right != " b" {
		t.Fatalf("Cursor steht falsch: links %q, rechts %q", left, right)
	}
}

func TestManualTriggerInsertsColon(t *testing.T) {
	m := model{ta: textarea.New()}
	m.ta.SetWidth(80)
	m.ta.SetValue("hallo welt")
	m.ta.SetCursor(5) // hinter 'hallo'

	m.openSuggestions()
	if got := m.ta.Value(); got != "hallo: welt" {
		t.Fatalf("Doppelpunkt nicht an der Cursorposition eingefügt: %q", got)
	}
	if len(m.sugg) == 0 {
		t.Fatal("Vorschläge nach manuellem Trigger nicht offen")
	}

	// Bei bereits offenem Fragment darf kein zweiter Doppelpunkt dazukommen.
	m.ta.SetValue("hallo :thu")
	m.ta.CursorEnd()
	m.openSuggestions()
	if got := m.ta.Value(); got != "hallo :thu" {
		t.Fatalf("Text bei offenem Fragment verändert: %q", got)
	}
	if len(m.sugg) == 0 {
		t.Fatal("Vorschläge bei offenem Fragment nicht geöffnet")
	}
}

func TestTmuxBadge(t *testing.T) {
	if got := tmuxWindowName(0); got != "rocketchat" {
		t.Fatalf("ohne Ungelesene erwartet 'rocketchat', bekam %q", got)
	}
	if got := tmuxWindowName(7); got != "rocketchat ●7" {
		t.Fatalf("Bobble falsch: %q", got)
	}

	// Die Summe aller ungelesenen Nachrichten zählt, nicht die Zahl der Räume.
	defer func(v bool) { inTmux = v }(inTmux)
	inTmux = true // Kommandos werden im Test nicht ausgeführt, nur geprüft

	m := model{rooms: []Room{{ID: "1", Unread: 3}, {ID: "2", Unread: 4}}}
	if cmd := m.badgeCmd(); cmd == nil {
		t.Fatal("erste Meldung muss raus")
	}
	if m.badge != 7 {
		t.Fatalf("Summe falsch: %d", m.badge)
	}
	if cmd := m.badgeCmd(); cmd != nil {
		t.Fatal("unveränderte Summe darf tmux nicht erneut anfassen")
	}
	m.rooms[0].Unread = 0
	if cmd := m.badgeCmd(); cmd == nil || m.badge != 4 {
		t.Fatalf("Änderung nicht gemeldet: badge %d", m.badge)
	}
}

func TestTmuxArgvTargetsOwnPane(t *testing.T) {
	defer func(v string) { tmuxPane = v }(tmuxPane)

	tmuxPane = "%7"
	got := strings.Join(tmuxArgv("rename-window", "rocketchat ●2"), " ")
	if got != "rename-window -t %7 rocketchat ●2" {
		t.Fatalf("Ziel fehlt oder steht falsch: %q", got)
	}
	got = strings.Join(tmuxArgv("set-window-option", "automatic-rename", "on"), " ")
	if got != "set-window-option -t %7 automatic-rename on" {
		t.Fatalf("Option falsch aufgebaut: %q", got)
	}

	// Ohne TMUX_PANE kein -t, sonst wäre das Argument leer.
	tmuxPane = ""
	if got := strings.Join(tmuxArgv("rename-window", "x"), " "); got != "rename-window x" {
		t.Fatalf("ohne Pane erwartet 'rename-window x', bekam %q", got)
	}
}

func TestListRowsFitWidth(t *testing.T) {
	m := model{
		w: 40, h: 20,
		rooms: []Room{
			{ID: "1", Type: "c", FName: "team_legacy_redesign", Unread: 6},
			{ID: "2", Type: "d", Name: "eva", FName: "Eva Beispiel"},
		},
	}
	m.enterSection(1)
	const w = 24
	for _, line := range strings.Split(m.viewList(w, 10), "\n") {
		if got := lipgloss.Width(line); got > w-1 {
			t.Fatalf("Zeile %q ist %d Spalten breit, erlaubt sind %d", stripStyle(line), got, w-1)
		}
	}
	// Zähler muss in derselben Zeile wie der Raumname stehen.
	first := stripStyle(strings.Split(m.viewList(w, 10), "\n")[0])
	if !strings.Contains(first, "6") {
		t.Fatalf("Zähler nicht in der Raumzeile: %q", first)
	}
}

// Kein Frame darf breiter oder höher als das Terminal sein — sonst umbricht eine Zeile,
// das Bild wird zu hoch und die Oberfläche scrollt bei jedem Tastendruck weg.
// Emoji mit Hautton sind der Auslöser: lipgloss zählt sie schmaler, als sie erscheinen.
func TestFramePasstInsTerminal(t *testing.T) {
	msg := Message{ID: "a", RID: "1", Msg: "hallo 🙏🏻 welt 👨‍👩‍👧 " + strings.Repeat("lang ", 40)}
	msg.U.Username = "eva"
	for _, size := range [][2]int{{40, 12}, {80, 24}, {120, 20}, {200, 40}} {
		w, h := size[0], size[1]
		for _, f := range []focusMode{focusList, focusMsgs, focusCompose, focusReact} {
			m := newModel(Config{URL: "https://chat.example.com"})
			m.w, m.h = w, h
			m.rooms = []Room{{ID: "1", Type: "c", FName: "team 🙏🏽", Unread: 2}}
			m.open = &m.rooms[0]
			m.msgs["1"] = []Message{msg}
			m.focus, m.rfilter = f, "pra"
			m.layout()
			m.renderOpen()
			lines := strings.Split(m.View(), "\n")
			if len(lines) > h {
				t.Fatalf("%dx%d focus=%d: %d Zeilen statt höchstens %d", w, h, f, len(lines), h)
			}
			for i, l := range lines {
				if got := cellWidth(stripStyle(l)); got > w {
					t.Fatalf("%dx%d focus=%d: Zeile %d ist %d Spalten breit: %q", w, h, f, i, got, stripStyle(l))
				}
			}
		}
	}
}

// Beim Öffnen eines Raums muss die neueste Nachricht markiert sein — auch wenn im Raum
// Thread-Antworten liegen, die in der Hauptansicht ausgefiltert werden.
func TestRaumOeffnenMarkiertLetzteNachricht(t *testing.T) {
	mk := func(id, tmid string) Message {
		x := Message{ID: id, RID: "1", Msg: "text " + id, TMID: tmid}
		x.U.Username = "eva"
		return x
	}
	m := newModel(Config{URL: "https://chat.example.com"})
	m.w, m.h = 100, 24
	m.rooms = []Room{{ID: "1", Type: "c", FName: "team"}}
	m.msgs["1"] = []Message{mk("a", ""), mk("b", ""), mk("reply1", "a"), mk("reply2", "a")}
	m.focus = focusMsgs
	m.layout()
	m.selectRoom(m.rooms[0])

	if m.msgCursor != len(m.rend.msgs)-1 {
		t.Fatalf("Cursor steht auf %d, erwartet %d", m.msgCursor, len(m.rend.msgs)-1)
	}
	if !strings.Contains(m.rend.content, "▎") {
		t.Fatalf("keine Nachricht markiert:\n%s", stripStyle(m.rend.content))
	}
	last := m.rend.starts[m.msgCursor]
	lines := strings.Split(m.rend.content, "\n")
	if !strings.Contains(stripStyle(lines[last]), "▎") {
		t.Fatalf("markiert ist nicht die letzte Nachricht: %q", stripStyle(lines[last]))
	}
}

// In der Suche bewegen ctrl+j/k den Trefferzeiger, ohne im Suchtext zu landen.
func TestSucheCtrlJKBewegtTreffer(t *testing.T) {
	m := newModel(Config{URL: "https://chat.example.com"})
	m.w, m.h = 100, 24
	m.focus = focusSearch
	m.ti.SetValue("eva")
	m.slUsers = []SpotlightUser{{Username: "eva"}, {Username: "evi"}, {Username: "evo"}}
	m.rebuild()
	start := m.rcursor

	press := func(t tea.KeyType) {
		next, _ := m.onSearchKey(tea.KeyMsg(tea.Key{Type: t}))
		m = next.(model)
	}
	press(tea.KeyCtrlJ)
	if m.rcursor <= start {
		t.Fatalf("ctrl+j bewegt den Zeiger nicht: %d → %d", start, m.rcursor)
	}
	press(tea.KeyCtrlK)
	if m.rcursor != start {
		t.Fatalf("ctrl+k geht nicht zurück: %d statt %d", m.rcursor, start)
	}
	if m.ti.Value() != "eva" {
		t.Fatalf("Suchtext verändert: %q", m.ti.Value())
	}
}

func TestWrappedLines(t *testing.T) {
	for _, tc := range []struct {
		s    string
		w, n int
	}{
		{"", 10, 1},
		{"kurz", 10, 1},
		{"exakt zehn", 10, 1},
		{"elf zeichen", 10, 2},
		{"a\nb", 10, 2},
		{"1234567890123456789012", 10, 3},
	} {
		if got := wrappedLines(tc.s, tc.w); got != tc.n {
			t.Errorf("wrappedLines(%q, %d) = %d, want %d", tc.s, tc.w, got, tc.n)
		}
	}
}

func TestAttachmentLinkPrefersOriginal(t *testing.T) {
	// RC-Upload: title_link ist das Original, image_url das 480er-Thumbnail.
	up := Attachment{TitleLink: "/file-upload/orig/bild.png", ImageURL: "/file-upload/thumb/bild.png"}
	if got := up.Link(); got != "/file-upload/orig/bild.png" {
		t.Fatalf("Upload: Thumbnail statt Original: %q", got)
	}
	// Nur ein Bild ohne title_link (z.B. Link-Preview) -- image_url ist alles, was da ist.
	if got := (Attachment{ImageURL: "https://x/y.png"}).Link(); got != "https://x/y.png" {
		t.Fatalf("Preview: %q", got)
	}
	if got := (Attachment{}).Link(); got != "" {
		t.Fatalf("leer: %q", got)
	}
}

// DDP füllt m.msgs auch für nie geöffnete Räume. Früher galt der Map-Eintrag als
// "History schon da" und der Verlauf wurde nie geholt — sichtbar blieb genau die
// eine live eingetroffene Nachricht.
func TestHistoryTrotzLiveNachrichtGeladen(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "history") {
			w.Write([]byte(`{"success":true}`))
			return
		}
		// REST liefert neueste zuerst
		w.Write([]byte(`{"messages":[
			{"_id":"alt2","rid":"1","msg":"zwei","ts":"2026-09-10T10:00:00.000Z"},
			{"_id":"alt1","rid":"1","msg":"eins","ts":"2026-09-09T10:00:00.000Z"}]}`))
	}))
	defer srv.Close()

	m := newModel(Config{URL: srv.URL, UserID: "u", Token: "t"})
	m.w, m.h = 100, 24
	m.layout()
	m.rooms = []Room{{ID: "1", Type: "c", FName: "team"}}
	live := Message{ID: "live", RID: "1", Msg: "frisch", TS: rcTime{time.Date(2026, 9, 17, 8, 0, 0, 0, time.UTC)}}
	m.msgs["1"] = []Message{live}

	for _, msg := range runCmd(t, m.selectRoom(m.rooms[0])) {
		next, _ := m.update(msg)
		m = next.(model)
	}

	if got := len(m.msgs["1"]); got != 3 {
		t.Fatalf("Raum hat %d Nachrichten, erwartet 3 (2 History + 1 live): %+v", got, m.msgs["1"])
	}
	if m.msgs["1"][0].ID != "alt1" || m.msgs["1"][2].ID != "live" {
		t.Fatalf("falsche Reihenfolge nach dem Merge: %s, %s, %s",
			m.msgs["1"][0].ID, m.msgs["1"][1].ID, m.msgs["1"][2].ID)
	}
}

// Nachgeladene ältere Nachrichten dürfen den Cursor nicht verschieben: er bleibt
// auf der Nachricht, die vorher oben stand.
func TestNachladenHaeltDenCursor(t *testing.T) {
	mk := func(id string, day int) Message {
		x := Message{ID: id, RID: "1", Msg: "text " + id,
			TS: rcTime{time.Date(2026, 9, day, 10, 0, 0, 0, time.UTC)}}
		x.U.Username = "eva"
		return x
	}
	m := newModel(Config{URL: "https://chat.example.com"})
	m.w, m.h = 100, 24
	m.layout()
	m.rooms = []Room{{ID: "1", Type: "c", FName: "team"}}
	m.open = &m.rooms[0]
	m.loaded["1"] = true
	m.msgs["1"] = []Message{mk("b", 10), mk("c", 11)}
	m.focus = focusMsgs
	m.msgCursor = 0
	m.renderOpen()

	next, _ := m.update(historyMsg{rid: "1", msgs: []Message{mk("a", 9)}, anchor: "b"})
	m = next.(model)

	if len(m.rend.msgs) != 3 {
		t.Fatalf("sichtbar sind %d Nachrichten, erwartet 3", len(m.rend.msgs))
	}
	if m.rend.msgs[m.msgCursor].ID != "b" {
		t.Fatalf("Cursor steht auf %q, erwartet \"b\"", m.rend.msgs[m.msgCursor].ID)
	}
}

// runCmd führt cmd aus und liefert alle Nachrichten, auch aus tea.Batch.
func runCmd(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, runCmd(t, c)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

// In einem Raum sucht "/" Nachrichten dieses Raums — nicht Räume und Personen.
func TestSlashImRaumSuchtNachrichten(t *testing.T) {
	m := newModel(Config{URL: "https://chat.example.com"})
	m.w, m.h = 120, 24
	m.layout()
	m.rooms = []Room{{ID: "1", Type: "c", FName: "hotline"}}
	m.open = &m.rooms[0]
	m.loaded["1"] = true
	m.focus = focusMsgs

	next, _ := m.onKey(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'/'}}))
	m = next.(model)
	if !m.roomSearch || m.focus != focusSearch {
		t.Fatalf("/ öffnet keine Raumsuche: roomSearch=%v focus=%v", m.roomSearch, m.focus)
	}

	hit := Message{ID: "h1", RID: "1", Msg: "der beas läuft nicht",
		TS: rcTime{time.Date(2026, 9, 11, 8, 31, 0, 0, time.UTC)}}
	hit.U.Username = "benedikt"
	m.ti.SetValue("beas")
	m.hits = []Message{hit}
	m.slUsers = []SpotlightUser{{Username: "eva"}} // darf nicht auftauchen
	m.slRooms = []SpotlightRoom{{ID: "9", Name: "irgendwas", Type: "c"}}
	m.rebuild()

	if len(m.results) != 1 || m.results[0].kind != resMsg {
		t.Fatalf("Ergebnisliste enthält nicht nur Nachrichten: %+v", m.results)
	}
	body := stripStyle(m.rend.content)
	if !strings.Contains(body, "der beas läuft nicht") || !strings.Contains(body, "1 Treffer") {
		t.Fatalf("Treffer stehen nicht im Nachrichtenbereich:\n%s", body)
	}
	if strings.Contains(body, "eva") || strings.Contains(body, "irgendwas") {
		t.Fatalf("Spotlight-Treffer in der Raumsuche:\n%s", body)
	}
	if !strings.Contains(m.rend.content, sHit.Render("beas")) {
		t.Fatalf("Suchwort nicht hervorgehoben")
	}
}

func TestMarkHits(t *testing.T) {
	cases := []struct{ text, query, want string }{
		{"beas läuft", "beas", sHit.Render("beas") + " läuft"},
		{"BEAS läuft", "beas", sHit.Render("BEAS") + " läuft"},
		{"beas läuft", "from:me", "beas läuft"}, // Operator ist kein Suchwort
		{"beas läuft", "a", "beas läuft"},       // zu kurz
		{"beas beas", "beas", sHit.Render("beas") + " " + sHit.Render("beas")},
	}
	for _, c := range cases {
		if got := markHits(c.text, c.query); got != c.want {
			t.Errorf("markHits(%q, %q) = %q, erwartet %q", c.text, c.query, got, c.want)
		}
	}
}

// e bearbeitet die eigene Nachricht, r reagiert, fremde Nachrichten bleiben unangetastet.
func TestEigeneNachrichtBearbeiten(t *testing.T) {
	mk := func(id, uid, text string) Message {
		x := Message{ID: id, RID: "1", Msg: text,
			TS: rcTime{time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)}}
		x.U.ID, x.U.Username = uid, "u"+uid
		return x
	}
	base := func() model {
		m := newModel(Config{URL: "https://chat.example.com"})
		m.w, m.h = 100, 24
		m.layout()
		m.myID, m.myUser = "me", "ume"
		m.rooms = []Room{{ID: "1", Type: "c", FName: "team"}}
		m.open = &m.rooms[0]
		m.loaded["1"] = true
		m.msgs["1"] = []Message{mk("fremd", "eva", "nicht meins"), mk("meins", "me", "mein text")}
		m.focus = focusMsgs
		m.renderOpen()
		return m
	}
	press := func(m model, r rune) model {
		next, _ := m.onKey(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{r}}))
		return next.(model)
	}

	m := base()
	m.msgCursor = 1 // eigene Nachricht
	m = press(m, 'e')
	if m.editing == nil || m.editing.ID != "meins" {
		t.Fatalf("e startet keine Bearbeitung: %+v", m.editing)
	}
	if m.focus != focusCompose || m.ta.Value() != "mein text" {
		t.Fatalf("Compose-Feld nicht vorbelegt: focus=%v value=%q", m.focus, m.ta.Value())
	}

	// esc verwirft und lässt das Feld leer für die nächste Nachricht
	next, _ := m.onComposeKey(tea.KeyMsg(tea.Key{Type: tea.KeyEsc}))
	m = next.(model)
	if m.editing != nil || m.ta.Value() != "" {
		t.Fatalf("esc räumt die Bearbeitung nicht ab: editing=%v value=%q", m.editing, m.ta.Value())
	}

	// fremde Nachricht: keine Bearbeitung, Hinweis in der Statuszeile
	m = base()
	m.msgCursor = 0
	m = press(m, 'e')
	if m.editing != nil || m.focus != focusMsgs {
		t.Fatalf("fremde Nachricht wurde zur Bearbeitung geöffnet")
	}
	if !strings.Contains(m.status, "eigene") {
		t.Fatalf("kein Hinweis in der Statuszeile: %q", m.status)
	}

	// r bleibt das Reagieren
	m = base()
	m.msgCursor = 1
	m = press(m, 'r')
	if m.focus != focusReact {
		t.Fatalf("r öffnet den Reaktions-Picker nicht: focus=%v", m.focus)
	}
}

// esc auf einem normalen Entwurf darf den Text nicht wegwerfen.
func TestEscBehaeltEntwurf(t *testing.T) {
	m := newModel(Config{URL: "https://chat.example.com"})
	m.w, m.h = 100, 24
	m.layout()
	m.rooms = []Room{{ID: "1", Type: "c", FName: "team"}}
	m.open = &m.rooms[0]
	m.focus = focusCompose
	m.ta.SetValue("halb getippt")

	next, _ := m.onComposeKey(tea.KeyMsg(tea.Key{Type: tea.KeyEsc}))
	m = next.(model)
	if m.ta.Value() != "halb getippt" {
		t.Fatalf("Entwurf verloren: %q", m.ta.Value())
	}
}

// Bearbeitete Nachrichten sind als solche erkennbar.
func TestBearbeitetMarker(t *testing.T) {
	msg := Message{ID: "a", RID: "1", Msg: "text",
		TS:       rcTime{time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)},
		EditedAt: rcTime{time.Date(2026, 9, 20, 9, 5, 0, 0, time.UTC)}}
	msg.U.Username = "eva"
	out := renderMessages([]Message{msg}, renderOpts{width: 60, cursor: -1})
	if !strings.Contains(stripStyle(out.content), "(bearbeitet)") {
		t.Fatalf("kein Marker:\n%s", stripStyle(out.content))
	}
}

// Umfragen der Poll-App stecken in UIKit-Blocks. Die Fixture ist auf das
// Nötige gekürzte Echtdaten aus #schattendateisystem-hotline.
const pollJSON = `{
  "_id": "9ipmzTKhjzhK5TNJT", "rid": "6a6c7e7c28661c7c6fd7b85b",
  "msg": "Arbeitet jemand mit den CLI-Commands?",
  "ts": "2026-10-05T19:32:13.933Z", "editedAt": "2026-10-05T20:01:00.000Z",
  "u": {"_id": "poll", "username": "app.poll"},
  "blocks": [
    {"type":"section","blockId":"b0","appId":"app1",
     "text":{"type":"plain_text","text":"Arbeitet jemand mit den CLI-Commands?"},
     "accessory":{"type":"overflow","actionId":"finish"}},
    {"type":"divider","blockId":"b1","appId":"app1"},
    {"type":"section","blockId":"b2","appId":"app1",
     "text":{"type":"plain_text","text":"Ich gebe selber CLI Commands ein"},
     "accessory":{"type":"button","actionId":"vote","text":{"type":"plain_text","text":"Vote"},"value":"0"}},
    {"type":"context","blockId":"b3","appId":"app1",
     "elements":[{"type":"mrkdwn","text":"` + "`" + `███                 ` + "`" + ` 16.67% (1)"}]},
    {"type":"context","blockId":"b4","appId":"app1",
     "elements":[{"type":"mrkdwn","text":"1 vote - vondergruen"}]},
    {"type":"section","blockId":"b5","appId":"app1",
     "text":{"type":"plain_text","text":"Agent macht alles"},
     "accessory":{"type":"button","actionId":"vote","text":{"type":"plain_text","text":"Vote"},"value":"1"}},
    {"type":"context","blockId":"b6","appId":"app1",
     "elements":[{"type":"mrkdwn","text":"` + "`" + `████████████████    ` + "`" + ` 83.33% (5)"}]}
  ]
}`

func pollMessage(t *testing.T) Message {
	t.Helper()
	var m Message
	if err := json.Unmarshal([]byte(pollJSON), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestUmfrageParsenUndAnzeigen(t *testing.T) {
	m := pollMessage(t)
	opts := m.Poll()
	if len(opts) != 2 {
		t.Fatalf("%d Optionen, erwartet 2: %+v", len(opts), opts)
	}
	if opts[0].Value != "0" || opts[0].BlockID != "b2" || opts[0].ActionID != "vote" || opts[0].AppID != "app1" {
		t.Fatalf("Option 1 falsch zusammengesetzt: %+v", opts[0])
	}
	if opts[1].Label != "Agent macht alles" || opts[1].Value != "1" {
		t.Fatalf("Option 2 falsch: %+v", opts[1])
	}

	out := stripStyle(renderMessages([]Message{m}, renderOpts{width: 70, cursor: -1}).content)
	for _, want := range []string{
		"[1] Ich gebe selber CLI Commands ein",
		"16.67% (1)",
		"1 vote - vondergruen",
		"[2] Agent macht alles",
		"83.33% (5)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("%q fehlt in der Ausgabe:\n%s", want, out)
		}
	}
	// Die Frage steht im Nachrichtentext, der erste Block wiederholt sie nur.
	if strings.Count(out, "Arbeitet jemand mit den CLI-Commands?") != 1 {
		t.Fatalf("Frage doppelt gerendert:\n%s", out)
	}
	// Jede Stimme ist technisch eine Bearbeitung — das gehört nicht an die Umfrage.
	if strings.Contains(out, "(bearbeitet)") {
		t.Fatalf("Umfrage als bearbeitet markiert:\n%s", out)
	}
}

// 1-9 stimmt für die Option mit der angezeigten Nummer, höhere Ziffern tun nichts.
func TestZiffernTasteWaehltOption(t *testing.T) {
	m := newModel(Config{URL: "https://chat.example.com"})
	m.w, m.h = 100, 24
	m.layout()
	m.rooms = []Room{{ID: "6a6c7e7c28661c7c6fd7b85b", Type: "c", FName: "hotline"}}
	m.open = &m.rooms[0]
	m.loaded[m.open.ID] = true
	m.msgs[m.open.ID] = []Message{pollMessage(t)}
	m.focus = focusMsgs
	m.renderOpen()
	m.msgCursor = 0

	opts := m.rend.msgs[0].Poll()
	if len(opts) != 2 {
		t.Fatalf("Umfrage nicht im Modell")
	}
	for _, c := range []struct {
		key  rune
		want bool
	}{{'1', true}, {'2', true}, {'3', false}} {
		next, cmd := m.onKey(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{c.key}}))
		m = next.(model)
		if (cmd != nil) != c.want {
			t.Fatalf("Taste %q: cmd=%v, erwartet %v", c.key, cmd != nil, c.want)
		}
	}
}

func TestBlockTextBeideFormen(t *testing.T) {
	var b Block
	// Buttons tragen in elements ein Objekt statt eines Strings — das darf die
	// Nachricht nicht versenken.
	raw := `{"type":"actions","elements":[{"type":"button","text":{"type":"plain_text","text":"Vote"}}]}`
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		t.Fatalf("actions-Block nicht lesbar: %v", err)
	}
	if len(b.Elements) != 1 || b.Elements[0].Text != "Vote" {
		t.Fatalf("Button-Text nicht gelesen: %+v", b.Elements)
	}
}

// ctrl+k öffnet die Raum- und Personensuche aus jedem Fokus.
func TestCtrlKOeffnetSucheUeberall(t *testing.T) {
	for _, f := range []focusMode{focusList, focusMsgs, focusCompose, focusReact} {
		m := newModel(Config{URL: "https://chat.example.com"})
		m.w, m.h = 100, 24
		m.layout()
		m.rooms = []Room{{ID: "1", Type: "c", FName: "team"}}
		m.open = &m.rooms[0]
		m.loaded["1"] = true
		m.focus = f
		m.roomSearch = true // eine laufende Raumsuche muss weichen

		next, _ := m.onKey(tea.KeyMsg(tea.Key{Type: tea.KeyCtrlK}))
		m = next.(model)
		if m.focus != focusSearch || m.roomSearch {
			t.Fatalf("aus focus %v: focus=%v roomSearch=%v", f, m.focus, m.roomSearch)
		}
	}

	// In der Suche bleibt ctrl+k der Trefferzeiger.
	m := newModel(Config{URL: "https://chat.example.com"})
	m.w, m.h = 100, 24
	m.layout()
	m.focus = focusSearch
	m.ti.SetValue("ev")
	m.slUsers = []SpotlightUser{{Username: "eva"}, {Username: "evi"}}
	m.rebuild()
	m.moveResult(1)
	before := m.rcursor
	next, _ := m.onKey(tea.KeyMsg(tea.Key{Type: tea.KeyCtrlK}))
	m = next.(model)
	if m.rcursor >= before {
		t.Fatalf("ctrl+k bewegt den Trefferzeiger nicht mehr: %d → %d", before, m.rcursor)
	}
}

// @-Fragment schlägt Leute aus dem Raumverlauf vor, tab setzt @username ein.
func TestMentionAutocomplete(t *testing.T) {
	mk := func(user, name string) Message {
		x := Message{ID: user, RID: "1", Msg: "hallo"}
		x.U.ID, x.U.Username, x.U.Name = user, user, name
		return x
	}
	m := newModel(Config{URL: "https://chat.example.com"})
	m.w, m.h = 100, 24
	m.layout()
	m.myID, m.myUser = "me", "ume"
	m.rooms = []Room{{ID: "1", Type: "c", FName: "team"}}
	m.open = &m.rooms[0]
	m.loaded["1"] = true
	m.msgs["1"] = []Message{mk("sanatgar", "Ali Sanatgar"), mk("steffel", "Martin Steffel"), mk("ume", "Ich")}
	m.focus = focusCompose

	m.ta.SetValue("kannst du das @stef")
	m.ta.CursorEnd()
	m.updateSuggestions()
	if len(m.sugg) == 0 || m.sugg[0].Glyph != "@steffel" {
		t.Fatalf("kein Treffer für @stef: %+v", m.sugg)
	}
	if m.suggTrigger != '@' {
		t.Fatalf("falscher Trigger: %q", m.suggTrigger)
	}
	m.acceptEmoji()
	if m.ta.Value() != "kannst du das @steffel " {
		t.Fatalf("Einsetzen falsch: %q", m.ta.Value())
	}

	// Treffer über den Anzeigenamen, eigener Name fliegt raus, @all ist dabei.
	m.ta.SetValue("@Ali")
	m.ta.CursorEnd()
	m.updateSuggestions()
	if len(m.sugg) == 0 || m.sugg[0].Glyph != "@sanatgar" {
		t.Fatalf("Anzeigename greift nicht: %+v", m.sugg)
	}
	for _, e := range m.mentionCandidates("", 20) {
		if e.Glyph == "@ume" {
			t.Fatal("eigener Benutzername wird vorgeschlagen")
		}
	}
	if m.mentionCandidates("al", 20)[0].Glyph != "@all" && m.mentionCandidates("al", 20)[1].Glyph != "@all" {
		t.Fatalf("@all fehlt: %+v", m.mentionCandidates("al", 20))
	}

	// Emoji muss weiter funktionieren.
	m.ta.SetValue("hallo :smil")
	m.ta.CursorEnd()
	m.updateSuggestions()
	if m.suggTrigger != ':' || len(m.sugg) == 0 {
		t.Fatalf("Emoji-Vorschläge kaputt: trigger=%q %+v", m.suggTrigger, m.sugg)
	}
}

// Nachrichten, die unterhalb des Sichtfelds eintreffen, werden angezeigt.
func TestNeueNachrichtenUnterhalbGezaehlt(t *testing.T) {
	mk := func(id string, min int) Message {
		x := Message{ID: id, RID: "1", Msg: "Zeile " + id,
			TS: rcTime{time.Date(2026, 10, 28, 9, min, 0, 0, time.UTC)}}
		x.U.ID, x.U.Username = "eva", "eva"
		return x
	}
	m := newModel(Config{URL: "https://chat.example.com"})
	m.w, m.h = 100, 14
	m.layout()
	m.myID, m.myUser = "me", "ume"
	m.rooms = []Room{{ID: "1", Type: "c", FName: "team"}}
	m.open = &m.rooms[0]
	m.loaded["1"] = true
	var list []Message
	for i := 1; i <= 10; i++ {
		list = append(list, mk(fmt.Sprintf("m%d", i), i))
	}
	m.msgs["1"] = list
	m.focus = focusCompose
	m.msgCursor = 2 // hochgescrollt
	m.renderOpen()

	next, _ := m.update(newMessageMsg{mk("neu", 59)})
	m = next.(model)
	if m.below != 1 {
		t.Fatalf("below=%d, erwartet 1", m.below)
	}
	if !strings.Contains(stripStyle(m.viewFooter()), "1 neu") {
		t.Fatalf("Hinweis fehlt im Footer:\n%s", stripStyle(m.viewFooter()))
	}

	// Ans Ende springen räumt den Zähler ab.
	m.focus = focusMsgs
	next, _ = m.onKey(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'G'}}))
	m = next.(model)
	if m.below != 0 {
		t.Fatalf("below bleibt nach G bei %d", m.below)
	}
}

// Diktiertext landet am Cursor und öffnet dabei das Eingabefeld.
func TestDiktatEinfuegen(t *testing.T) {
	m := newModel(Config{URL: "https://chat.example.com"})
	m.w, m.h = 100, 24
	m.layout()
	m.rooms = []Room{{ID: "1", Type: "c", FName: "team"}}
	m.open = &m.rooms[0]
	m.loaded["1"] = true
	m.focus = focusMsgs

	next, _ := m.update(sttMsg{text: "der BEAS läuft wieder"})
	m = next.(model)
	if m.focus != focusCompose {
		t.Fatalf("Eingabefeld nicht geöffnet: focus=%v", m.focus)
	}
	if m.ta.Value() != "der BEAS läuft wieder" {
		t.Fatalf("Text falsch eingefügt: %q", m.ta.Value())
	}

	// Zweites Diktat hängt mit Leerzeichen an, ohne doppeltes.
	next, _ = m.update(sttMsg{text: "Tenant neu gestartet"})
	m = next.(model)
	if m.ta.Value() != "der BEAS läuft wieder Tenant neu gestartet" {
		t.Fatalf("Anhängen falsch: %q", m.ta.Value())
	}

	// Leeres Ergebnis und Fehler landen in der Statuszeile, nicht im Text.
	before := m.ta.Value()
	next, _ = m.update(sttMsg{text: ""})
	m = next.(model)
	if m.ta.Value() != before || !strings.Contains(m.status, "nichts erkannt") {
		t.Fatalf("leeres Ergebnis falsch behandelt: %q / %q", m.ta.Value(), m.status)
	}
	next, _ = m.update(sttMsg{err: errors.New("rc-tui-stt: kaputt")})
	m = next.(model)
	if m.ta.Value() != before || !m.statusErr {
		t.Fatalf("Fehler falsch behandelt: %q / %v", m.ta.Value(), m.statusErr)
	}
	if m.dictStatus != "" {
		t.Fatalf("Diktatstatus bleibt stehen: %q", m.dictStatus)
	}
}

// Das Eingabefeld wächst mit dem Fenster, nicht nur bis zur alten Grenze von 6.
func TestEingabefeldWaechstMitFenster(t *testing.T) {
	langerText := strings.Repeat("diktierter Satz, der umbricht. ", 80)
	cases := []struct{ h, wantMin, wantMax int }{
		{24, 7, 11}, // halbe Höhe
		{50, 20, 24},
		{14, 6, 6}, // kleines Fenster behält die Untergrenze
	}
	for _, c := range cases {
		m := newModel(Config{URL: "https://chat.example.com"})
		m.w, m.h = 100, c.h
		m.layout()
		m.rooms = []Room{{ID: "1", Type: "c", FName: "team"}}
		m.open = &m.rooms[0]
		m.loaded["1"] = true
		m.focus = focusCompose
		m.ta.SetValue(langerText)
		m.growCompose()

		if got := m.ta.Height(); got < c.wantMin || got > c.wantMax {
			t.Errorf("h=%d: Eingabefeld %d Zeilen, erwartet %d..%d", c.h, got, c.wantMin, c.wantMax)
		}
		// Der Nachrichtenbereich darf dabei nicht verschwinden.
		if m.vp.Height < 3 {
			t.Errorf("h=%d: Nachrichtenbereich auf %d Zeilen geschrumpft", c.h, m.vp.Height)
		}
	}

	// Ein Resize zieht die Höhe nach.
	m := newModel(Config{URL: "https://chat.example.com"})
	m.w, m.h = 100, 14
	m.layout()
	m.focus = focusCompose
	m.ta.SetValue(langerText)
	m.growCompose()
	klein := m.ta.Height()
	next, _ := m.update(tea.WindowSizeMsg{Width: 100, Height: 60})
	m = next.(model)
	if m.ta.Height() <= klein {
		t.Fatalf("nach dem Vergrößern immer noch %d Zeilen (vorher %d)", m.ta.Height(), klein)
	}
}

// Nach dem Senden ist der Schreibmodus verlassen — globale Tasten greifen sofort.
func TestSendenVerlaesstSchreibmodus(t *testing.T) {
	m := newModel(Config{URL: "https://chat.example.com"})
	m.w, m.h = 100, 24
	m.layout()
	m.rooms = []Room{{ID: "1", Type: "c", FName: "team"}}
	m.open = &m.rooms[0]
	m.loaded["1"] = true
	m.focus = focusCompose
	m.ta.SetValue("fertig")
	m.ta.Focus()

	next, cmd := m.onComposeKey(tea.KeyMsg(tea.Key{Type: tea.KeyEnter}))
	m = next.(model)
	if cmd == nil {
		t.Fatal("es wird gar nicht gesendet")
	}
	if m.focus != focusMsgs {
		t.Fatalf("Fokus nach dem Senden: %v, erwartet focusMsgs", m.focus)
	}
	if m.ta.Value() != "" || m.ta.Focused() {
		t.Fatalf("Eingabefeld nicht abgeräumt: %q focused=%v", m.ta.Value(), m.ta.Focused())
	}
	// ctrl+k muss jetzt direkt die Suche öffnen, statt ins Textfeld zu tippen.
	next, _ = m.onKey(tea.KeyMsg(tea.Key{Type: tea.KeyCtrlK}))
	if next.(model).focus != focusSearch {
		t.Fatal("ctrl+k greift nach dem Senden nicht")
	}
}

// ctrl+w pendelt zwischen Raumliste und offenem Chat.
func TestCtrlWPendelt(t *testing.T) {
	mk := func(id string) Message {
		x := Message{ID: id, RID: "1", Msg: "text " + id,
			TS: rcTime{time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)}}
		x.U.ID, x.U.Username = "eva", "eva"
		return x
	}
	m := newModel(Config{URL: "https://chat.example.com"})
	m.w, m.h = 100, 24
	m.layout()
	m.rooms = []Room{{ID: "1", Type: "c", FName: "team"}}
	m.open = &m.rooms[0]
	m.loaded["1"] = true
	m.msgs["1"] = []Message{mk("a"), mk("b"), mk("c")}
	m.focus = focusMsgs
	m.renderOpen()

	press := func(mm model) model {
		next, _ := mm.onKey(tea.KeyMsg(tea.Key{Type: tea.KeyCtrlW}))
		return next.(model)
	}
	m = press(m)
	if m.focus != focusList {
		t.Fatalf("erstes ctrl+w landet nicht links: %v", m.focus)
	}
	m = press(m)
	if m.focus != focusMsgs {
		t.Fatalf("zweites ctrl+w kommt nicht zurück in den Chat: %v", m.focus)
	}
	if m.msgCursor != len(m.rend.msgs)-1 {
		t.Fatalf("Cursor steht auf %d statt am Ende (%d)", m.msgCursor, len(m.rend.msgs)-1)
	}

	// Ohne offenen Raum bleibt ctrl+w der Sprung nach links.
	m.open = nil
	m.focus = focusList
	m = press(m)
	if m.focus != focusList {
		t.Fatalf("ohne offenen Raum darf ctrl+w nicht nach rechts: %v", m.focus)
	}
}

func TestKopierenInsClipboard(t *testing.T) {
	mk := func(id, text string) Message {
		x := Message{ID: id, RID: "1", Msg: text,
			TS: rcTime{time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)}}
		x.U.ID, x.U.Username = "eva", "eva"
		return x
	}
	var copied []string
	old := copyFn
	copyFn = func(s string) error { copied = append(copied, s); return nil }
	defer func() { copyFn = old }()

	m := newModel(Config{URL: "https://chat.example.com"})
	m.w, m.h = 100, 24
	m.layout()
	m.rooms = []Room{{ID: "1", Type: "c", FName: "team"}}
	m.open = &m.rooms[0]
	m.loaded["1"] = true
	m.msgs["1"] = []Message{
		mk("a", "einfach nur text"),
		mk("b", "schau mal:\n```sh\nsystemctl restart be-portal\n```\nund noch\n```\necho zwei\n```"),
		mk("c", ""),
	}
	m.focus = focusMsgs
	m.renderOpen()
	press := func(m model, r rune) model {
		next, _ := m.onKey(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{r}}))
		return next.(model)
	}

	// y kopiert den Rohtext, nicht das gerenderte Layout.
	m.msgCursor = 0
	m = press(m, 'y')
	if len(copied) != 1 || copied[0] != "einfach nur text" {
		t.Fatalf("y kopiert falsch: %q", copied)
	}

	// Y nimmt alle Codeblöcke ohne Zäune und ohne Sprachangabe.
	copied = nil
	m.msgCursor = 1
	m = press(m, 'Y')
	want := "systemctl restart be-portal\n\necho zwei"
	if len(copied) != 1 || copied[0] != want {
		t.Fatalf("Y kopiert %q, erwartet %q", copied, want)
	}
	if !strings.Contains(m.status, "2 Codeblöcke") {
		t.Fatalf("Status nennt die Anzahl nicht: %q", m.status)
	}

	// y auf einer Nachricht ohne Text fasst das Clipboard nicht an.
	copied = nil
	m.msgCursor = 2
	m = press(m, 'y')
	if len(copied) != 0 {
		t.Fatalf("leere Nachricht kopiert: %q", copied)
	}
	// Y ohne Codeblock ebenso.
	m.msgCursor = 0
	m = press(m, 'Y')
	if len(copied) != 0 {
		t.Fatalf("Y ohne Codeblock kopiert: %q", copied)
	}
}

func TestCodeBlocksUngeschlossen(t *testing.T) {
	got := codeBlocks("vorher\n```go\nfmt.Println(1)")
	if len(got) != 1 || got[0] != "fmt.Println(1)" {
		t.Fatalf("offener Block nicht erkannt: %q", got)
	}
	if n := len(codeBlocks("ganz ohne")); n != 0 {
		t.Fatalf("Text ohne Zäune liefert %d Blöcke", n)
	}
}
