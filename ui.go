package main

import (
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kyokomi/emoji/v2"
	"github.com/sahilm/fuzzy"
)

// --- messages ---

type roomsMsg struct {
	rooms []Room
	err   error
}
type historyMsg struct {
	rid  string
	msgs []Message
	// anchor ist die Nachricht, auf der der Cursor danach stehen soll (leer = ans Ende).
	anchor   string
	atOldest bool
	err      error
}
type meMsg struct {
	id, username string
	err          error
}
type newMessageMsg struct{ m Message }
type subChangedMsg struct{ r Room }
type errMsg struct{ err error }
type noteMsg string
type ddpStatusMsg struct {
	Connected bool
	Err       string
}
type spotlightMsg struct {
	seq   int
	users []SpotlightUser
	rooms []SpotlightRoom
	err   error
}
type msgSearchMsg struct {
	seq  int
	hits []Message
	err  error
}
type searchTickMsg struct{ seq int }
type previewTickMsg struct{ seq int }
type openedRoomMsg struct {
	r   Room
	err error
}
type threadMsg struct {
	msgs []Message
	err  error
}

// --- styles ---

var (
	cAccent = lipgloss.Color("#a78bfa")
	cDim    = lipgloss.Color("#6b7280")
	cText   = lipgloss.Color("#d4d4d8")
	cWarn   = lipgloss.Color("#f59e0b")
	cErr    = lipgloss.Color("#f87171")
	cOK     = lipgloss.Color("#4ade80")

	sTitle    = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	sDim      = lipgloss.NewStyle().Foreground(cDim)
	sTab      = lipgloss.NewStyle().Foreground(cDim).Padding(0, 1)
	sTabOn    = lipgloss.NewStyle().Foreground(cAccent).Bold(true).Underline(true).Padding(0, 1)
	sSel      = lipgloss.NewStyle().Foreground(lipgloss.Color("#18181b")).Background(cAccent)
	sUnread   = lipgloss.NewStyle().Foreground(cWarn).Bold(true)
	sAuthor   = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	sBody     = lipgloss.NewStyle().Foreground(cText)
	sGroup    = lipgloss.NewStyle().Foreground(cWarn).Bold(true)
	sDivider  = lipgloss.NewStyle().Foreground(cDim).Border(lipgloss.NormalBorder(), false, true, false, false).BorderForeground(cDim)
	sReaction = lipgloss.NewStyle().Foreground(cOK)
	sMine     = lipgloss.NewStyle().Foreground(cOK).Bold(true)
	sBar      = lipgloss.NewStyle().Foreground(cAccent)
	sErrTxt   = lipgloss.NewStyle().Foreground(cErr)
	sHit      = lipgloss.NewStyle().Foreground(lipgloss.Color("#18181b")).Background(cWarn)
	sCode     = lipgloss.NewStyle().Foreground(lipgloss.Color("#fde68a")).Background(lipgloss.Color("#3f3f46"))
	sCodeBlk  = lipgloss.NewStyle().Foreground(lipgloss.Color("#e4e4e7")).Background(lipgloss.Color("#27272a")).
			Border(lipgloss.NormalBorder(), false, false, false, true).BorderForeground(cAccent).Padding(0, 1)
)

// userPalette colours participant names in rooms with more than two people.
var userPalette = []lipgloss.Color{
	"#f472b6", "#60a5fa", "#fbbf24", "#22d3ee", "#e879f9",
	"#fb923c", "#93c5fd", "#fca5a5", "#a5b4fc", "#d8b4fe",
}

// userStyle maps a username to a stable colour — same person, same colour, no request.
func userStyle(username string) lipgloss.Style {
	h := fnv.New32a()
	h.Write([]byte(username))
	return lipgloss.NewStyle().Foreground(userPalette[int(h.Sum32())%len(userPalette)]).Bold(true)
}

type emojiEntry struct{ Code, Glyph string }

var (
	allEmojiOnce sync.Once
	allEmoji     []emojiEntry
)

// emojiCatalogue lists every shortcode of the emoji package, prefix-sorted.
func emojiCatalogue() []emojiEntry {
	allEmojiOnce.Do(func() {
		for code, glyph := range emoji.CodeMap() {
			allEmoji = append(allEmoji, emojiEntry{strings.Trim(code, ":"), strings.TrimSpace(glyph)})
		}
		sort.Slice(allEmoji, func(i, j int) bool { return allEmoji[i].Code < allEmoji[j].Code })
	})
	return allEmoji
}

// findEmoji ranks prefix matches before substring matches.
func findEmoji(frag string, limit int) []emojiEntry {
	frag = strings.ToLower(strings.Trim(frag, ": "))
	if frag == "" {
		return quickEmoji
	}
	var pre, sub []emojiEntry
	for _, e := range emojiCatalogue() {
		switch {
		case strings.HasPrefix(e.Code, frag):
			pre = append(pre, e)
		case strings.Contains(e.Code, frag):
			sub = append(sub, e)
		}
		if len(pre) >= limit {
			break
		}
	}
	out := append(pre, sub...)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// reactions offered by the picker; anything else can be typed as :shortcode:.
// ponytail: 20 hartcodierte Reaktionen statt Emoji-Dependency, freie Eingabe deckt den Rest.
var quickEmoji = []emojiEntry{
	{"thumbsup", "👍"}, {"thumbsdown", "👎"}, {"heart", "❤️"}, {"smile", "😄"},
	{"joy", "😂"}, {"tada", "🎉"}, {"eyes", "👀"}, {"rocket", "🚀"},
	{"fire", "🔥"}, {"ok_hand", "👌"}, {"clap", "👏"}, {"pray", "🙏"},
	{"thinking", "🤔"}, {"white_check_mark", "✅"}, {"x", "❌"}, {"warning", "⚠️"},
	{"100", "💯"}, {"wave", "👋"}, {"raised_hands", "🙌"}, {"sob", "😭"},
}

// --- model ---

type section struct {
	name string
	keep func(Room) bool
}

var sections = []section{
	{"Unread", func(r Room) bool { return r.Unread > 0 || r.Alert }},
	{"Channels", func(r Room) bool { return r.Type == "c" || r.Type == "p" }},
	{"DMs", func(r Room) bool { return r.Type == "d" }},
}

type resKind int

const (
	resHeader resKind = iota
	resRoom
	resUser
	resMsg
)

type result struct {
	kind  resKind
	label string
	room  Room
	user  SpotlightUser
	msg   Message
}

type focusMode int

const (
	focusList focusMode = iota
	focusMsgs
	focusCompose
	focusReact
	focusSearch
)

const searchPlaceholder = "Person, Team, Channel, Nachricht…"
const composePlaceholder = "Nachricht… (alt+enter neue Zeile)"

const (
	histPage     = 80 // Nachrichten pro History-Request
	histMaxPages = 5  // Seiten, die ein Nachladen höchstens durchläuft
)

// countRoots counts the messages of a page that the room view shows at all.
func countRoots(msgs []Message) int {
	n := 0
	for _, x := range msgs {
		if x.TMID == "" {
			n++
		}
	}
	return n
}

// rendered holds a formatted room history plus the line offset of each message,
// so the viewport can follow the message cursor.
type rendered struct {
	content string
	msgs    []Message
	starts  []int
}

type model struct {
	cfg  Config
	api  *Client
	w, h int

	myID   string
	myUser string

	rooms []Room
	msgs  map[string][]Message
	// loaded trennt "History geholt" von "Map hat einen Eintrag" -- DDP schreibt
	// auch in Räume, die nie offen waren, und die brauchen trotzdem ihre History.
	loaded   map[string]bool
	atOldest map[string]bool
	loading  bool
	sec      int
	sticky   map[string]bool // Räume, die in der Unread-Section stehen bleiben dürfen
	cursor   int
	open     *Room
	vp       viewport.Model

	focus     focusMode
	rend      rendered
	msgCursor int
	thread    *Message // gesetzt = Thread-Ansicht statt Raumverlauf

	ti      textinput.Model // Suche
	ta      textarea.Model  // Nachricht verfassen (mehrzeilig)
	editing *Message        // gesetzt = das Compose-Feld bearbeitet diese Nachricht
	rfilter string          // Reaktions-Filter

	sugg        []emojiEntry // Autocomplete im Text: Emoji oder Mention
	scurs       int
	suggTrigger byte // ':' oder '@' — bestimmt, was acceptEmoji ersetzt

	// below zählt Nachrichten, die unterhalb des Sichtfelds eingetroffen sind.
	below int

	rec        *exec.Cmd // läuft = Diktat nimmt gerade auf
	recPath    string
	dictStatus string

	// roomSearch = Suche im offenen Raum statt Räume/Personen: kein Spotlight,
	// die Treffer stehen im Nachrichtenbereich statt in der Seitenleiste.
	roomSearch bool

	sseq    int
	pseq    int
	slUsers []SpotlightUser
	slRooms []SpotlightRoom
	hits    []Message
	results []result
	rcursor int
	ecursor int

	connected bool
	status    string
	statusErr bool
	badge     int // letzte an tmux gemeldete Ungelesen-Summe
}

func newModel(cfg Config) model {
	ti := textinput.New()
	ti.Prompt = "🔍 "
	ti.Placeholder = searchPlaceholder
	ta := textarea.New()
	ta.Prompt = "› "
	ta.Placeholder = composePlaceholder
	ta.CharLimit = 4000
	ta.ShowLineNumbers = false
	ta.SetHeight(1)
	// enter sendet, Zeilenumbruch kommt auf alt+enter.
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter"))
	return model{
		cfg:      cfg,
		api:      NewClient(cfg),
		msgs:     map[string][]Message{},
		loaded:   map[string]bool{},
		atOldest: map[string]bool{},
		ti:       ti,
		ta:       ta,
		vp:       viewport.New(80, 20),
		status:   "lade…",
	}
}

func (m model) Init() tea.Cmd { return tea.Batch(m.fetchRooms, m.fetchMe, tmuxBadge(0)) }

// --- commands ---

func (m model) fetchRooms() tea.Msg {
	r, err := m.api.Subscriptions()
	return roomsMsg{r, err}
}

func (m model) fetchMe() tea.Msg {
	id, user, _, err := m.api.Me()
	return meMsg{id, user, err}
}

func (m model) fetchHistory(r Room) tea.Cmd {
	return func() tea.Msg {
		msgs, err := m.api.History(r, histPage, time.Time{})
		return historyMsg{rid: r.ID, msgs: msgs, err: err}
	}
}

// fetchOlder loads pages before a timestamp until one of them carries something
// the room view actually shows: Thread-Antworten werden dort ausgefiltert, und
// eine ganze Seite kann aus nichts anderem bestehen.
// ponytail: Deckel bei histMaxPages, sonst läuft ein Thread-Monolog die Historie hoch.
func (m model) fetchOlder(r Room, before time.Time, anchor string) tea.Cmd {
	return func() tea.Msg {
		var all []Message
		for page := 0; page < histMaxPages; page++ {
			msgs, err := m.api.History(r, histPage, before)
			if err != nil {
				return historyMsg{rid: r.ID, err: err}
			}
			if len(msgs) == 0 {
				return historyMsg{rid: r.ID, msgs: all, anchor: anchor, atOldest: true}
			}
			all = append(msgs, all...)
			before = msgs[0].TS.Time
			if countRoots(msgs) > 0 {
				break
			}
		}
		return historyMsg{rid: r.ID, msgs: all, anchor: anchor}
	}
}

func (m model) markRead(rid string) tea.Cmd {
	return func() tea.Msg {
		m.api.do("POST", "subscriptions.read", nil, map[string]string{"rid": rid}, nil)
		return nil
	}
}

func (m model) send(rid, text, tmid string) tea.Cmd {
	return func() tea.Msg {
		msg, err := m.api.PostMessage(rid, text, tmid)
		if err != nil {
			return errMsg{err}
		}
		return newMessageMsg{msg}
	}
}

func (m model) edit(rid, msgID, text string) tea.Cmd {
	return func() tea.Msg {
		msg, err := m.api.UpdateMessage(rid, msgID, text)
		if err != nil {
			return errMsg{err}
		}
		return newMessageMsg{msg} // ersetzt die Nachricht über ihre ID, wie beim Senden
	}
}

func (m model) vote(o PollOption, msg Message) tea.Cmd {
	return func() tea.Msg {
		if err := m.api.Vote(o, msg.ID, msg.RID); err != nil {
			return errMsg{err}
		}
		return noteMsg("Stimme für „" + o.Label + "“ abgegeben")
	}
}

func (m model) react(msgID, code string, on bool) tea.Cmd {
	return func() tea.Msg {
		if err := m.api.React(msgID, ":"+code+":", on); err != nil {
			return errMsg{err}
		}
		return nil // DDP spiegelt die aktualisierte Nachricht zurück
	}
}

func (m model) fetchThread(tmid string) tea.Cmd {
	return func() tea.Msg {
		msgs, err := m.api.ThreadMessages(tmid, 100)
		return threadMsg{msgs, err}
	}
}

func (m model) doSpotlight(seq int, q string) tea.Cmd {
	return func() tea.Msg {
		u, r, err := m.api.Spotlight(q)
		return spotlightMsg{seq, u, r, err}
	}
}

func (m model) doMsgSearch(seq int, rid, q string) tea.Cmd {
	return func() tea.Msg {
		h, err := m.api.SearchMessages(rid, q, 50)
		return msgSearchMsg{seq, h, err}
	}
}

func (m model) openDM(username string) tea.Cmd {
	return func() tea.Msg {
		r, err := m.api.OpenDM(username)
		return openedRoomMsg{r, err}
	}
}

func (m model) previewTick() tea.Cmd {
	seq := m.pseq
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return previewTickMsg{seq} })
}

// --- helpers on model ---

func (m model) visible() []Room {
	out := []Room{}
	for _, r := range m.rooms {
		// In der Unread-Section bleiben gelesene Räume stehen, bis man die Section verlässt.
		if m.sec == 0 && m.sticky[r.ID] || sections[m.sec].keep(r) {
			out = append(out, r)
		}
	}
	return out
}

// enterSection switches sections and pins whatever is currently unread.
func (m *model) enterSection(i int) {
	m.sec = i
	if i != 0 {
		m.sticky = nil
		return
	}
	m.sticky = map[string]bool{}
	m.pinUnread()
}

func (m *model) pinUnread() {
	if m.sec != 0 || m.sticky == nil {
		return
	}
	for _, r := range m.rooms {
		if r.Unread > 0 || r.Alert {
			m.sticky[r.ID] = true
		}
	}
}

// footerH is the footer height: input plus one hint line.
func (m model) footerH() int {
	if m.focus == focusCompose {
		return m.ta.Height() + 1
	}
	return 2
}

func (m *model) layout() {
	listW := 34
	if m.w < 100 {
		listW = max(22, m.w/3)
	}
	m.vp.Width = max(20, m.w-listW-3)
	m.vp.Height = max(3, m.h-2-m.footerH())
	m.ta.SetWidth(max(20, m.w-2))
}

// wrappedLines counts display rows for s when wrapped at width w.
func wrappedLines(s string, w int) int {
	if w < 1 {
		w = 1
	}
	n := 0
	for _, l := range strings.Split(s, "\n") {
		n += max(1, (lipgloss.Width(l)+w-1)/w)
	}
	return n
}

// stopEditing leaves edit mode and restores the compose field for new messages.
func (m *model) stopEditing() {
	if m.editing == nil {
		return // esc auf einem normalen Entwurf darf den Text nicht wegwerfen
	}
	m.editing = nil
	m.ta.Reset()
	m.ta.Placeholder = composePlaceholder
	m.growCompose()
}

// composeMax is how tall the input field may grow: half the window, never less
// than six rows. Fest auf 6 war zu wenig, sobald ein Diktat mehrere Zeilen bringt.
func (m model) composeMax() int {
	return max(6, (m.h-2)/2)
}

// growCompose keeps the textarea as tall as its content, up to composeMax rows.
func (m *model) growCompose() {
	h := clamp(wrappedLines(m.ta.Value(), m.ta.Width()), 1, m.composeMax())
	if h != m.ta.Height() {
		m.ta.SetHeight(h)
		m.layout()
		m.renderOpen()
	}
}

func (m *model) renderOpen() {
	if m.roomSearch && m.open != nil {
		m.renderHits()
		return
	}
	if m.open == nil {
		m.rend = rendered{}
		m.vp.SetContent(sDim.Render("\n  Raum wählen (enter) oder suchen (/)"))
		return
	}
	cur := -1
	if m.focus != focusList && m.focus != focusSearch {
		cur = m.msgCursor
	}
	o := renderOpts{width: m.vp.Width, cursor: cur, me: m.myUser, colored: m.open.Multi()}
	list := m.msgs[m.open.ID]
	if m.thread != nil {
		o.thread = true
		list = m.threadList()
	}
	m.rend = renderMessages(list, o)
	m.msgCursor = clamp(m.msgCursor, 0, max(0, len(m.rend.msgs)-1))
	if cur >= 0 && cur != m.msgCursor {
		// Der Index kam aus der Rohliste (mit Thread-Antworten) und zeigte daneben —
		// mit dem geklemmten Index nochmal rendern, sonst ist keine Nachricht markiert.
		o.cursor = m.msgCursor
		m.rend = renderMessages(list, o)
	}
	m.vp.SetContent(m.rend.content)
	if m.msgCursor >= len(m.rend.msgs)-1 {
		m.below = 0
	}
	if cur < 0 {
		m.vp.GotoBottom()
		return
	}
	m.followCursor()
}

// renderHits paints the search results of the open room into the message pane.
// Treffer stehen einzeilig -- die ganze Nachricht sieht man nach dem Sprung.
func (m *model) renderHits() {
	q := strings.TrimSpace(m.ti.Value())
	head := sTitle.Render("🔍 "+m.open.Icon()+m.open.Title()) +
		sDim.Render(fmt.Sprintf("  %d Treffer", len(m.hits)))
	if len(m.hits) == 0 {
		msg := "  tippen zum Suchen"
		if len(q) >= 2 {
			msg = "  keine Treffer für " + q
		}
		m.rend = rendered{}
		m.vp.SetContent(head + "\n" + sDim.Render(msg))
		return
	}
	out := rendered{}
	lines := []string{head}
	for _, h := range m.hits {
		style := sAuthor
		if m.open.Multi() {
			style = userStyle(h.U.Username)
		}
		text := truncate(oneLine(emoji.Sprint(h.Msg)), max(10, m.vp.Width-2))
		out.starts = append(out.starts, len(lines))
		out.msgs = append(out.msgs, h)
		lines = append(lines, "",
			style.Render(h.Author())+" "+sDim.Render(h.TS.Format("Mon, 02.01.2006 15:04")),
			"  "+markHits(text, q))
	}
	if m.rcursor >= 0 && m.rcursor < len(out.starts) {
		from := out.starts[m.rcursor]
		to := len(lines)
		if m.rcursor+1 < len(out.starts) {
			to = out.starts[m.rcursor+1]
		}
		for i := from; i < to; i++ {
			if strings.TrimSpace(stripStyle(lines[i])) == "" {
				continue
			}
			lines[i] = sBar.Render("▎") + lines[i]
		}
	}
	out.content = strings.Join(lines, "\n")
	m.rend = out
	m.vp.SetContent(out.content)
	m.followIndex(m.rcursor)
}

// markHits highlights the search words in a hit. Operatoren wie from:me stehen
// nicht im Text und bleiben außen vor.
// ponytail: ein Durchlauf über den Rohtext -- nachträglich eingefügte Farbcodes
// würden sonst selbst wieder als Treffer gelten.
func markHits(s, q string) string {
	var words []string
	for _, w := range strings.Fields(strings.ToLower(q)) {
		if len([]rune(w)) < 2 || strings.Contains(w, ":") {
			continue
		}
		words = append(words, w)
	}
	if len(words) == 0 {
		return s
	}
	low := strings.ToLower(s)
	if len(low) != len(s) {
		return s // ToLower hat die Bytelänge verschoben, Offsets passen nicht mehr
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		hit := 0
		for _, w := range words {
			if len(w) > hit && strings.HasPrefix(low[i:], w) {
				hit = len(w)
			}
		}
		if hit == 0 {
			b.WriteByte(s[i])
			i++
			continue
		}
		b.WriteString(sHit.Render(s[i : i+hit]))
		i += hit
	}
	return b.String()
}

// threadList returns the thread parent followed by its replies, from the room store.
func (m model) threadList() []Message {
	if m.thread == nil || m.open == nil {
		return nil
	}
	out := []Message{}
	for _, x := range m.msgs[m.open.ID] {
		if x.ID == m.thread.ID {
			out = append([]Message{x}, out...)
			continue
		}
		if x.TMID == m.thread.ID {
			out = append(out, x)
		}
	}
	if len(out) == 0 || out[0].ID != m.thread.ID {
		out = append([]Message{*m.thread}, out...)
	}
	return out
}

func (m *model) openThread(parent Message) tea.Cmd {
	p := parent
	m.thread = &p
	m.msgCursor = 0
	m.renderOpen()
	m.msgCursor = max(0, len(m.rend.msgs)-1)
	m.renderOpen()
	return m.fetchThread(p.ID)
}

func (m *model) closeThread() {
	id := ""
	if m.thread != nil {
		id = m.thread.ID
	}
	m.thread = nil
	m.renderOpen()
	for i, x := range m.rend.msgs {
		if x.ID == id {
			m.msgCursor = i
			m.renderOpen()
			return
		}
	}
}

func (m *model) followCursor() { m.followIndex(m.msgCursor) }

func (m *model) followIndex(i int) {
	if i < 0 || i >= len(m.rend.starts) {
		return
	}
	top := m.rend.starts[i]
	bottom := lipgloss.Height(m.rend.content)
	if i+1 < len(m.rend.starts) {
		bottom = m.rend.starts[i+1]
	}
	if top < m.vp.YOffset {
		m.vp.SetYOffset(top)
	} else if bottom > m.vp.YOffset+m.vp.Height {
		m.vp.SetYOffset(bottom - m.vp.Height)
	}
}

// mergeMsgs folds a fetched page into the room store: gleiche IDs werden
// ersetzt, der Rest einsortiert. Ohne das würde eine nachgeladene Seite die
// live über DDP eingetroffenen Nachrichten überschreiben.
func (m *model) mergeMsgs(rid string, msgs []Message) {
	if len(msgs) == 0 {
		return
	}
	list := m.msgs[rid]
	at := make(map[string]int, len(list))
	for i := range list {
		at[list[i].ID] = i
	}
	for _, x := range msgs {
		if i, ok := at[x.ID]; ok {
			list[i] = x
			continue
		}
		at[x.ID] = len(list)
		list = append(list, x)
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].TS.Before(list[j].TS.Time) })
	m.msgs[rid] = list
}

// loadOlder fetches the page before the oldest known message of the open room.
func (m *model) loadOlder() tea.Cmd {
	if m.open == nil || m.thread != nil || m.loading || m.atOldest[m.open.ID] {
		return nil
	}
	list := m.msgs[m.open.ID]
	if len(list) == 0 {
		return nil
	}
	anchor := ""
	if len(m.rend.msgs) > 0 {
		anchor = m.rend.msgs[0].ID
	}
	m.loading = true
	m.note("lade ältere…")
	return m.fetchOlder(*m.open, list[0].TS.Time, anchor)
}

func (m *model) selectRoom(r Room) tea.Cmd {
	m.thread = nil
	m.open = &r
	if !hasRoom(m.rooms, r.ID) {
		m.rooms = append([]Room{r}, m.rooms...)
	}
	for i := range m.rooms {
		if m.rooms[i].ID == r.ID {
			m.rooms[i].Unread, m.rooms[i].Alert = 0, false
		}
	}
	m.msgCursor = max(0, len(m.msgs[r.ID])-1)
	m.renderOpen()
	cmds := []tea.Cmd{m.markRead(r.ID)}
	if !m.loaded[r.ID] && !m.loading {
		m.status = "lade " + r.Title() + "…"
		m.loading = true
		cmds = append(cmds, m.fetchHistory(r))
	}
	return tea.Batch(cmds...)
}

// focusSection switches the sidebar to the section containing r and puts the cursor on it.
func (m *model) focusSection(id string) {
	for si, s := range sections {
		for _, r := range m.rooms {
			if r.ID != id || !s.keep(r) {
				continue
			}
			if si == 0 {
				continue // Unread ist kein Zuhause für einen gerade gelesenen Raum
			}
			m.enterSection(si)
			for i, v := range m.visible() {
				if v.ID == id {
					m.cursor = i
					return
				}
			}
			return
		}
	}
}

func (m *model) fail(err error) {
	m.status, m.statusErr = err.Error(), true
}

func (m *model) note(s string) {
	m.status, m.statusErr = s, false
}

// --- update ---

// Update wraps update so every state change can refresh the tmux badge in one place.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	mm := next.(model)
	if b := mm.badgeCmd(); b != nil {
		return mm, tea.Batch(cmd, b)
	}
	return mm, cmd
}

func (m model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.layout()
		m.growCompose() // die Obergrenze hängt an der Fensterhöhe
		m.renderOpen()
		return m, nil

	case tea.KeyMsg:
		return m.onKey(msg)

	case meMsg:
		if msg.err != nil {
			m.fail(msg.err)
			return m, nil
		}
		m.myID, m.myUser = msg.id, msg.username
		return m, nil

	case errMsg:
		m.fail(msg.err)
		return m, nil

	case noteMsg:
		m.note(string(msg))
		return m, nil

	case sttMsg:
		m.dictStatus = ""
		if msg.err != nil {
			m.fail(msg.err)
			return m, nil
		}
		if msg.text == "" {
			m.note("nichts erkannt")
			return m, nil
		}
		m.note("")
		return m, m.insertDictation(msg.text)

	case roomsMsg:
		if msg.err != nil {
			m.fail(msg.err)
			return m, nil
		}
		m.rooms = msg.rooms
		if m.sticky == nil && m.sec == 0 {
			m.sticky = map[string]bool{}
		}
		m.pinUnread()
		m.note(fmt.Sprintf("%d Räume", len(m.rooms)))
		if m.open == nil && len(m.visible()) > 0 {
			return m, m.previewTick()
		}
		return m, nil

	case historyMsg:
		m.loading = false
		if msg.err != nil {
			m.fail(msg.err)
			return m, nil
		}
		first := !m.loaded[msg.rid]
		m.loaded[msg.rid] = true
		if msg.atOldest {
			m.atOldest[msg.rid] = true
		}
		m.mergeMsgs(msg.rid, msg.msgs)
		if m.open == nil || m.open.ID != msg.rid {
			return m, nil
		}
		m.note("")
		switch {
		case msg.anchor != "":
			m.renderOpen() // erst rendern, dann den Anker in der sichtbaren Liste suchen
			for i, x := range m.rend.msgs {
				if x.ID == msg.anchor {
					m.msgCursor = i
					break
				}
			}
		case first:
			m.msgCursor = max(0, len(m.msgs[msg.rid])-1)
		}
		m.renderOpen()
		return m, nil

	case newMessageMsg:
		rid := msg.m.RID
		list := m.msgs[rid]
		replaced := false
		for i := range list {
			if list[i].ID == msg.m.ID {
				list[i], replaced = msg.m, true
				break
			}
		}
		if !replaced {
			list = append(list, msg.m)
		}
		m.msgs[rid] = list
		if m.open != nil && m.open.ID == rid {
			atEnd := m.msgCursor >= len(m.rend.msgs)-1
			if !replaced && !atEnd && msg.m.U.ID != m.myID {
				m.below++ // steht unterhalb des Sichtfelds, sonst fällt sie niemandem auf
			}
			m.renderOpen()
			if !replaced && atEnd {
				m.msgCursor = max(0, len(m.rend.msgs)-1)
				m.renderOpen()
			}
			if !replaced {
				return m, m.markRead(rid)
			}
			return m, nil
		}
		if !replaced && msg.m.U.ID != m.myID {
			for i := range m.rooms {
				if m.rooms[i].ID == rid {
					m.rooms[i].Unread++
					m.rooms[i].LastMsg = msg.m.TS
					break
				}
			}
			m.pinUnread()
		}
		return m, nil

	case subChangedMsg:
		for i := range m.rooms {
			if m.rooms[i].ID == msg.r.ID {
				m.rooms[i].Unread, m.rooms[i].Alert = msg.r.Unread, msg.r.Alert
				m.pinUnread()
				return m, nil
			}
		}
		if msg.r.Open {
			m.rooms = append([]Room{msg.r}, m.rooms...)
		}
		return m, nil

	case ddpStatusMsg:
		m.connected = msg.Connected
		if msg.Err != "" {
			m.note("live: " + msg.Err)
		}
		return m, nil

	case searchTickMsg:
		if m.focus != focusSearch || msg.seq != m.sseq {
			return m, nil
		}
		q := strings.TrimSpace(m.ti.Value())
		if len(q) < 2 {
			return m, nil
		}
		if m.roomSearch {
			if m.open == nil {
				return m, nil
			}
			return m, m.doMsgSearch(msg.seq, m.open.ID, q)
		}
		cmds := []tea.Cmd{m.doSpotlight(msg.seq, q)}
		if m.open != nil {
			cmds = append(cmds, m.doMsgSearch(msg.seq, m.open.ID, q))
		}
		return m, tea.Batch(cmds...)

	case previewTickMsg:
		if msg.seq != m.pseq || m.focus != focusList {
			return m, nil
		}
		vis := m.visible()
		if len(vis) == 0 {
			return m, nil
		}
		return m, m.selectRoom(vis[min(m.cursor, len(vis)-1)])

	case spotlightMsg:
		if msg.seq != m.sseq {
			return m, nil
		}
		m.slUsers, m.slRooms = msg.users, msg.rooms
		m.rebuild()
		return m, nil

	case msgSearchMsg:
		if msg.seq != m.sseq {
			return m, nil
		}
		m.hits = msg.hits
		m.rebuild()
		return m, nil

	case threadMsg:
		if msg.err != nil {
			m.fail(msg.err)
			return m, nil
		}
		for _, x := range msg.msgs {
			list := m.msgs[x.RID]
			found := false
			for i := range list {
				if list[i].ID == x.ID {
					list[i], found = x, true
					break
				}
			}
			if !found {
				list = append(list, x)
			}
			m.msgs[x.RID] = list
		}
		if m.thread != nil {
			m.msgCursor = max(0, len(m.threadList())-1)
			m.renderOpen()
		}
		return m, nil

	case openedRoomMsg:
		if msg.err != nil {
			m.fail(msg.err)
			return m, nil
		}
		cmd := m.selectRoom(msg.r)
		m.focusSection(msg.r.ID)
		return m, cmd
	}
	return m, nil
}

func (m model) onKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "ctrl+c":
		m.killRecording()
		return m, tea.Quit
	case "ctrl+d":
		if m.rec != nil {
			return m, m.stopRecording()
		}
		if m.open == nil {
			m.note("erst einen Raum öffnen")
			return m, nil
		}
		m.startRecording()
		return m, nil
	case "ctrl+k":
		// In der Suche selbst bleibt ctrl+k der Trefferzeiger (neben ctrl+p und up).
		if m.focus != focusSearch {
			return m, m.openRoomSearch()
		}
	case "ctrl+w":
		if m.focus == focusList && m.open != nil {
			// Schon links: zurück in den offenen Chat, ans Ende — dorthin, wo die
			// Vorschau gerade stand. Der Raum muss dafür nicht in der Liste stehen.
			m.focus = focusMsgs
			m.msgCursor = max(0, len(m.rend.msgs)-1)
			m.renderOpen()
			return m, nil
		}
		m.thread = nil
		m.roomSearch = false
		m.focus = focusList
		m.ta.Blur()
		m.stopEditing()
		m.ti.Blur()
		m.sugg, m.scurs = nil, 0
		m.slUsers, m.slRooms, m.hits = nil, nil, nil
		m.enterSection(0)
		m.cursor, m.pseq = 0, m.pseq+1
		m.layout()
		m.rebuild()
		m.renderOpen()
		return m, m.previewTick()
	}
	switch m.focus {
	case focusSearch:
		return m.onSearchKey(k)
	case focusCompose:
		return m.onComposeKey(k)
	case focusReact:
		return m.onReactKey(k)
	case focusMsgs:
		return m.onMsgKey(k)
	}
	return m.onListKey(k)
}

// openRoomSearch switches to the Räume-und-Personen-Suche, no matter where the
// focus was. Ein laufender Entwurf bleibt im Eingabefeld stehen.
func (m *model) openRoomSearch() tea.Cmd {
	m.roomSearch = false
	m.focus = focusSearch
	m.ta.Blur()
	m.sugg, m.scurs = nil, 0
	m.ti.SetValue("")
	m.ti.Placeholder = searchPlaceholder
	m.ti.Focus()
	m.slUsers, m.slRooms, m.hits = nil, nil, nil
	m.rcursor = 0
	m.layout()
	m.rebuild()
	m.renderOpen()
	return textinput.Blink
}

func (m model) onListKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "q":
		return m, tea.Quit
	case "/":
		return m, m.openRoomSearch()
	case "tab", "l", "right":
		m.enterSection((m.sec + 1) % len(sections))
		m.cursor, m.pseq = 0, m.pseq+1
		return m, m.previewTick()
	case "shift+tab", "h", "left":
		m.enterSection((m.sec - 1 + len(sections)) % len(sections))
		m.cursor, m.pseq = 0, m.pseq+1
		return m, m.previewTick()
	case "j", "down":
		if m.cursor < len(m.visible())-1 {
			m.cursor, m.pseq = m.cursor+1, m.pseq+1
			return m, m.previewTick()
		}
		return m, nil
	case "k", "up":
		if m.cursor > 0 {
			m.cursor, m.pseq = m.cursor-1, m.pseq+1
			return m, m.previewTick()
		}
		return m, nil
	case "enter":
		vis := m.visible()
		if len(vis) == 0 {
			return m, nil
		}
		cmd := m.selectRoom(vis[min(m.cursor, len(vis)-1)])
		m.focus = focusMsgs
		m.renderOpen()
		return m, cmd
	case "i":
		if m.open == nil {
			return m, nil
		}
		m.focus = focusCompose
		m.sugg, m.scurs = nil, 0
		m.layout()
		m.renderOpen()
		return m, m.ta.Focus()
	case "r":
		m.note("aktualisiere…")
		return m, m.fetchRooms
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(k)
	return m, cmd
}

func (m model) onMsgKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		if m.thread != nil {
			m.closeThread()
			return m, nil
		}
		m.focus = focusList
		m.renderOpen()
		return m, nil
	case "q":
		return m, tea.Quit
	case "t":
		if len(m.rend.msgs) == 0 || m.msgCursor >= len(m.rend.msgs) {
			return m, nil
		}
		parent := m.rend.msgs[m.msgCursor]
		if m.thread != nil {
			return m, nil
		}
		if parent.TMID != "" {
			return m, nil
		}
		return m, m.openThread(parent)
	case "j", "down":
		if m.msgCursor < len(m.rend.msgs)-1 {
			m.msgCursor++
			m.renderOpen()
		}
		return m, nil
	case "k", "up":
		if m.msgCursor > 0 {
			m.msgCursor--
			m.renderOpen()
			return m, nil
		}
		return m, m.loadOlder()
	case "g":
		m.msgCursor = 0
		m.renderOpen()
		return m, nil
	case "G":
		m.msgCursor = max(0, len(m.rend.msgs)-1)
		m.renderOpen()
		return m, nil
	case "o":
		if m.msgCursor >= len(m.rend.msgs) {
			return m, nil
		}
		return m, m.openTarget(m.rend.msgs[m.msgCursor])
	case "i", "a":
		m.focus = focusCompose
		m.sugg, m.scurs = nil, 0
		m.layout()
		m.renderOpen()
		return m, m.ta.Focus()
	case "r":
		if len(m.rend.msgs) == 0 {
			return m, nil
		}
		m.focus = focusReact
		m.rfilter, m.ecursor = "", 0
		return m, nil
	case "e":
		if m.msgCursor >= len(m.rend.msgs) {
			return m, nil
		}
		target := m.rend.msgs[m.msgCursor]
		if target.U.ID != m.myID {
			m.note("nur eigene Nachrichten lassen sich bearbeiten")
			return m, nil
		}
		if target.Msg == "" {
			m.note("diese Nachricht hat keinen Text")
			return m, nil
		}
		m.editing = &target
		m.focus = focusCompose
		m.sugg, m.scurs = nil, 0
		m.ta.SetValue(target.Msg)
		m.ta.CursorEnd()
		m.ta.Placeholder = "bearbeiten…"
		m.growCompose()
		m.layout()
		m.renderOpen()
		return m, m.ta.Focus()
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		// ponytail: direkte Zifferntaste statt Auswahlmodus -- die Poll-App toggelt,
		// ein Fehlgriff ist mit derselben Taste wieder weg.
		if m.msgCursor >= len(m.rend.msgs) {
			return m, nil
		}
		target := m.rend.msgs[m.msgCursor]
		opts := target.Poll()
		i := int(k.String()[0] - '1')
		if i >= len(opts) {
			return m, nil
		}
		m.note("stimme ab…")
		return m, m.vote(opts[i], target)
	case "y":
		if m.msgCursor >= len(m.rend.msgs) {
			return m, nil
		}
		text := m.rend.msgs[m.msgCursor].Msg
		if strings.TrimSpace(text) == "" {
			m.note("diese Nachricht hat keinen Text")
			return m, nil
		}
		if err := copyFn(text); err != nil {
			m.fail(err)
			return m, nil
		}
		m.note(fmt.Sprintf("Nachricht kopiert (%d Zeichen)", len([]rune(text))))
		return m, nil
	case "Y":
		if m.msgCursor >= len(m.rend.msgs) {
			return m, nil
		}
		blocks := codeBlocks(m.rend.msgs[m.msgCursor].Msg)
		if len(blocks) == 0 {
			m.note("kein Codeblock in dieser Nachricht")
			return m, nil
		}
		// Mehrere Blöcke: alle, durch Leerzeile getrennt -- sonst müsste man
		// raten, welcher gemeint ist, und der Rest wäre still weg.
		if err := copyFn(strings.Join(blocks, "\n\n")); err != nil {
			m.fail(err)
			return m, nil
		}
		if len(blocks) == 1 {
			m.note("Codeblock kopiert")
		} else {
			m.note(fmt.Sprintf("%d Codeblöcke kopiert", len(blocks)))
		}
		return m, nil
	case "/":
		if m.open == nil {
			return m, nil
		}
		m.roomSearch = true
		m.focus = focusSearch
		m.ti.SetValue("")
		m.ti.Placeholder = "in " + m.open.Title() + " suchen…  (from:me, order:asc)"
		m.ti.Focus()
		m.hits, m.slUsers, m.slRooms = nil, nil, nil
		m.rcursor = 0
		m.rebuild()
		return m, textinput.Blink
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(k)
	if m.vp.AtTop() {
		return m, tea.Batch(cmd, m.loadOlder())
	}
	return m, cmd
}

func (m model) onComposeKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		if len(m.sugg) > 0 {
			m.sugg, m.scurs = nil, 0
			return m, nil
		}
		m.focus = focusMsgs
		m.ta.Blur()
		m.stopEditing()
		m.layout()
		m.renderOpen()
		return m, nil
	case "ctrl+e":
		// ponytail: überschreibt textareas ctrl+e (Zeilenende), "end" macht das weiter.
		m.openSuggestions()
		return m, nil
	case "tab":
		if len(m.sugg) > 0 {
			m.acceptEmoji()
			return m, nil
		}
		return m, nil
	case "left", "ctrl+p":
		if len(m.sugg) > 0 {
			m.scurs = max(0, m.scurs-1)
			return m, nil
		}
	case "right", "ctrl+n":
		if len(m.sugg) > 0 {
			m.scurs = min(len(m.sugg)-1, m.scurs+1)
			return m, nil
		}
	case "enter":
		if len(m.sugg) > 0 {
			m.acceptEmoji()
			return m, nil
		}
		text := strings.TrimSpace(m.ta.Value())
		if text == "" || m.open == nil {
			return m, nil
		}
		if m.editing != nil {
			target := *m.editing
			m.focus = focusMsgs
			m.ta.Blur()
			m.stopEditing()
			m.layout()
			m.renderOpen()
			if text == target.Msg {
				return m, nil // nichts geändert, kein Request
			}
			return m, m.edit(target.RID, target.ID, text)
		}
		tmid := ""
		if m.thread != nil {
			tmid = m.thread.ID
		}
		// Nach dem Senden zurück in die Nachrichtenansicht: sonst klebt man im
		// Schreibmodus und die globalen Tasten tippen sich in den nächsten Entwurf.
		m.ta.Reset()
		m.focus = focusMsgs
		m.ta.Blur()
		m.sugg, m.scurs = nil, 0
		m.growCompose()
		m.layout()
		m.renderOpen()
		return m, m.send(m.open.ID, text, tmid)
	}
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(k)
	m.growCompose()
	m.updateSuggestions()
	return m, cmd
}

var (
	// reEmojiFrag triggert automatisch: der Doppelpunkt muss am Wortanfang stehen,
	// damit "Uhrzeit:" keine Vorschläge öffnet.
	reEmojiFrag = regexp.MustCompile(`(^|\s):([a-z0-9_+-]*)$`)
	// reEmojiLoose gilt für den manuellen Trigger — dort ist die Absicht eindeutig.
	reEmojiLoose = regexp.MustCompile(`:([a-z0-9_+-]*)$`)
	// reMentionFrag funktioniert wie reEmojiFrag, nur für @.
	reMentionFrag = regexp.MustCompile(`(^|\s)@([a-zA-Z0-9._-]*)$`)
)

// mentionCandidates lists people from the open room, most recent speaker first.
// ponytail: nur wer hier geschrieben hat — channels.members oder spotlight, falls
// das irgendwann nicht reicht.
func (m model) mentionCandidates(frag string, limit int) []emojiEntry {
	frag = strings.ToLower(frag)
	seen := map[string]bool{m.myUser: true}
	var pre, sub []emojiEntry
	add := func(user, name string) {
		if user == "" || seen[user] {
			return
		}
		lu, ln := strings.ToLower(user), strings.ToLower(name)
		switch {
		case frag == "" || strings.HasPrefix(lu, frag) || strings.HasPrefix(ln, frag):
			seen[user] = true
			pre = append(pre, emojiEntry{Code: name, Glyph: "@" + user})
		case strings.Contains(lu, frag) || strings.Contains(ln, frag):
			seen[user] = true
			sub = append(sub, emojiEntry{Code: name, Glyph: "@" + user})
		}
	}
	if m.open != nil {
		list := m.msgs[m.open.ID]
		for i := len(list) - 1; i >= 0; i-- {
			add(list[i].U.Username, list[i].U.Name)
		}
	}
	add("all", "alle im Raum")
	add("here", "alle Anwesenden")
	out := append(pre, sub...)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// leftOfCursor splits the current row at the caret. LineInfo is rune-based,
// so the row has to be sliced as runes.
func (m model) leftOfCursor() (left, right string, rows []string, row int) {
	rows = strings.Split(m.ta.Value(), "\n")
	row = clamp(m.ta.Line(), 0, len(rows)-1)
	rr := []rune(rows[row])
	li := m.ta.LineInfo()
	col := clamp(li.StartColumn+li.ColumnOffset, 0, len(rr))
	return string(rr[:col]), string(rr[col:]), rows, row
}

// moveCursorTo places the caret after SetValue, which parks it at the end.
func (m *model) moveCursorTo(row, col int) {
	for guard := 0; m.ta.Line() > row && guard < 500; guard++ {
		m.ta.CursorUp()
	}
	m.ta.SetCursor(col)
}

// updateSuggestions offers completions while a ':' or '@' fragment sits left of the caret.
func (m *model) updateSuggestions() {
	left, _, _, _ := m.leftOfCursor()
	switch {
	case reEmojiFrag.MatchString(left):
		m.sugg, m.suggTrigger = findEmoji(reEmojiFrag.FindStringSubmatch(left)[2], 12), ':'
	case reMentionFrag.MatchString(left):
		m.sugg, m.suggTrigger = m.mentionCandidates(reMentionFrag.FindStringSubmatch(left)[2], 12), '@'
	default:
		m.sugg, m.scurs = nil, 0
		return
	}
	m.scurs = min(m.scurs, max(0, len(m.sugg)-1))
}

func (m *model) acceptEmoji() {
	if m.scurs >= len(m.sugg) {
		return
	}
	left, right, rows, row := m.leftOfCursor()
	i := strings.LastIndexByte(left, m.suggTrigger)
	if i < 0 {
		m.sugg, m.scurs = nil, 0
		return
	}
	// Nach einer Mention geht der Satz weiter, nach einem Emoji am Zeilenende nicht.
	tail := " "
	if strings.HasPrefix(right, " ") || m.suggTrigger == ':' && right == "" && row == len(rows)-1 {
		tail = ""
	}
	newLeft := left[:i] + m.sugg[m.scurs].Glyph + tail
	rows[row] = newLeft + right
	m.ta.SetValue(strings.Join(rows, "\n"))
	m.moveCursorTo(row, len([]rune(newLeft)))
	m.sugg, m.scurs = nil, 0
}

// openSuggestions is the manual trigger: without an open fragment it types the colon first.
func (m *model) openSuggestions() {
	left, right, rows, row := m.leftOfCursor()
	if !reEmojiLoose.MatchString(left) {
		left += ":"
		rows[row] = left + right
		m.ta.SetValue(strings.Join(rows, "\n"))
		m.moveCursorTo(row, len([]rune(left)))
	}
	mt := reEmojiLoose.FindStringSubmatch(left)
	m.sugg, m.scurs, m.suggTrigger = findEmoji(mt[1], 12), 0, ':'
}

func (m model) onReactKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	list := m.emojiChoices()
	switch k.String() {
	case "esc":
		m.focus = focusMsgs
		return m, nil
	case "left", "up", "shift+tab":
		m.ecursor = max(0, m.ecursor-1)
		return m, nil
	case "right", "down", "tab":
		m.ecursor = min(max(0, len(list)-1), m.ecursor+1)
		return m, nil
	case "backspace":
		if m.rfilter != "" {
			r := []rune(m.rfilter)
			m.rfilter = string(r[:len(r)-1])
			m.ecursor = 0
		}
		return m, nil
	case "enter":
		code := strings.Trim(m.rfilter, ": ")
		if len(list) > 0 && m.ecursor < len(list) {
			code = list[m.ecursor].Code
		}
		if code == "" || m.msgCursor >= len(m.rend.msgs) {
			return m, nil
		}
		target := m.rend.msgs[m.msgCursor]
		on := !hasReacted(target, code, m.myUser)
		m.focus = focusMsgs
		verb := "reagiert"
		if !on {
			verb = "Reaktion entfernt"
		}
		m.note(verb + " :" + code + ":")
		return m, m.react(target.ID, code, on)
	}
	if len(k.Runes) > 0 {
		m.rfilter += string(k.Runes)
		m.ecursor = 0
	}
	return m, nil
}

func (m model) emojiChoices() []emojiEntry {
	return findEmoji(m.rfilter, 20)
}

func hasReacted(m Message, code, user string) bool {
	r, ok := m.Reactions[":"+code+":"]
	if !ok {
		return false
	}
	for _, u := range r.Usernames {
		if u == user {
			return true
		}
	}
	return false
}

// openTarget opens the first attachment or link of a message in an external tool.
func (m model) openTarget(msg Message) tea.Cmd {
	for _, a := range msg.Attachments {
		link := a.Link()
		if link == "" {
			continue
		}
		if strings.HasPrefix(link, "http") {
			return openExternal(link)
		}
		// Uploads brauchen den Auth-Header, also erst laden, dann öffnen.
		return func() tea.Msg {
			f, err := m.api.Download(link)
			if err != nil {
				return errMsg{err}
			}
			return openExternal(f)()
		}
	}
	if u := reURL.FindString(msg.Msg); u != "" {
		return openExternal(u)
	}
	return func() tea.Msg { return errMsg{errors.New("in dieser Nachricht ist kein Link oder Anhang")} }
}

func openExternal(target string) tea.Cmd {
	return func() tea.Msg {
		// ponytail: xdg-open reicht auf Linux, macOS/Windows erst wenn jemand danach fragt.
		if err := exec.Command("xdg-open", target).Start(); err != nil {
			return errMsg{err}
		}
		return noteMsg("öffne " + target)
	}
}

var (
	reInlineCode = regexp.MustCompile("`([^`\n]+)`")
	reURL        = regexp.MustCompile(`https?://[^\s<>()\[\]"']+`)
	reFence      = regexp.MustCompile("^[ \t]*```")
)

// renderText formats one message body: code fences as blocks, inline backticks
// highlighted, emoji shortcodes resolved outside of code.
func renderText(text string, width int) string {
	w := max(10, width-4)
	var out []string
	var code []string
	inCode, lang := false, ""
	flush := func() {
		head := ""
		if lang != "" {
			head = sDim.Render(lang) + "\n"
		}
		out = append(out, sCodeBlk.Width(w-2).Render(head+strings.Join(code, "\n")))
		code, lang = nil, ""
	}
	for _, ln := range strings.Split(text, "\n") {
		if reFence.MatchString(ln) {
			if inCode {
				flush()
				inCode = false
			} else {
				inCode = true
				lang = strings.TrimSpace(strings.Trim(strings.TrimSpace(ln), "`"))
			}
			continue
		}
		if inCode {
			code = append(code, ln)
			continue
		}
		out = append(out, sBody.Width(w).Render(inlineText(ln)))
	}
	if inCode {
		flush() // unclosed fence: trotzdem als Block zeigen
	}
	return strings.Join(out, "\n")
}

func inlineText(s string) string {
	s = emoji.Sprint(s)
	return reInlineCode.ReplaceAllStringFunc(s, func(m string) string {
		return sCode.Render(" " + strings.Trim(m, "`") + " ")
	})
}

// --- search ---

func (m model) onSearchKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		if m.roomSearch {
			m.roomSearch = false
			m.focus = focusMsgs
			m.ti.Blur()
			m.hits, m.results = nil, nil
			m.renderOpen()
			return m, nil
		}
		m.focus = focusList
		m.ti.Blur()
		m.renderOpen()
		return m, nil
	case "up", "ctrl+p", "ctrl+k":
		m.moveResult(-1)
		if m.roomSearch {
			m.renderOpen()
		}
		return m, nil
	case "down", "ctrl+n", "ctrl+j":
		m.moveResult(1)
		if m.roomSearch {
			m.renderOpen()
		}
		return m, nil
	case "enter":
		return m.activate()
	}
	var cmd tea.Cmd
	before := m.ti.Value()
	m.ti, cmd = m.ti.Update(k)
	if m.ti.Value() != before {
		m.sseq++
		m.slUsers, m.slRooms, m.hits = nil, nil, nil
		m.rcursor = 0
		m.rebuild()
		seq := m.sseq
		cmd = tea.Batch(cmd, tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return searchTickMsg{seq} }))
	}
	return m, cmd
}

func (m *model) moveResult(d int) {
	for i := m.rcursor + d; i >= 0 && i < len(m.results); i += d {
		if m.results[i].kind != resHeader {
			m.rcursor = i
			return
		}
	}
}

func (m model) activate() (tea.Model, tea.Cmd) {
	if m.rcursor >= len(m.results) {
		return m, nil
	}
	r := m.results[m.rcursor]
	m.ti.Blur()
	switch r.kind {
	case resRoom:
		m.focus = focusMsgs
		cmd := m.selectRoom(r.room)
		m.focusSection(r.room.ID)
		return m, cmd
	case resUser:
		m.focus = focusList
		m.note("öffne DM mit " + r.user.Username + "…")
		return m, m.openDM(r.user.Username)
	case resMsg:
		m.roomSearch = false
		m.hits = nil
		for _, rr := range m.rooms {
			if rr.ID == r.msg.RID {
				m.focus = focusMsgs
				cmd := m.selectRoom(rr)
				m.focusSection(rr.ID)
				anchor := r.msg.ID
				if r.msg.TMID != "" {
					anchor = r.msg.TMID // Thread-Antworten stehen nicht im Raumverlauf
				}
				// ponytail: holt nur das Fenster vor dem Treffer. Liegt der weit zurück,
				// klafft zwischen ihm und dem aktuellen Ende eine Lücke -- sichtbar nur
				// am Datumstrenner. Lückenmarkierung, wenn das stört.
				return m, tea.Batch(cmd, m.fetchOlder(rr, r.msg.TS.Add(time.Millisecond), anchor))
			}
		}
	}
	return m, nil
}

// rebuild assembles the three-tier search result list.
func (m *model) rebuild() {
	q := strings.TrimSpace(m.ti.Value())
	var out []result

	if m.roomSearch {
		for _, h := range m.hits {
			out = append(out, result{kind: resMsg, msg: h})
		}
		m.results = out
		m.rcursor = clamp(m.rcursor, 0, max(0, len(out)-1))
		m.renderOpen()
		return
	}

	local := m.rooms
	if q != "" {
		matches := fuzzy.FindFrom(q, roomSource(m.rooms))
		local = nil
		for _, mt := range matches {
			local = append(local, m.rooms[mt.Index])
		}
	}
	if len(local) > 12 {
		local = local[:12]
	}
	if len(local) > 0 {
		out = append(out, result{kind: resHeader, label: "Deine Räume"})
		for _, r := range local {
			out = append(out, result{kind: resRoom, room: r, label: r.Icon() + " " + r.Title()})
		}
	}

	var srv []result
	known := map[string]bool{}
	for _, r := range m.rooms {
		known[r.ID] = true
		known[strings.ToLower(r.Name)] = true
	}
	for _, u := range m.slUsers {
		if known[strings.ToLower(u.Username)] {
			continue
		}
		label := "@" + u.Username
		if u.Name != "" {
			label += "  " + sDim.Render(u.Name)
		}
		srv = append(srv, result{kind: resUser, user: u, label: label})
	}
	for _, r := range m.slRooms {
		if known[r.ID] {
			continue
		}
		room := Room{ID: r.ID, Name: r.Name, FName: r.Name, Type: r.Type}
		srv = append(srv, result{kind: resRoom, room: room, label: room.Icon() + " " + r.Name})
	}
	if len(srv) > 10 {
		srv = srv[:10]
	}
	if len(srv) > 0 {
		out = append(out, result{kind: resHeader, label: "Server"})
		out = append(out, srv...)
	}

	if len(m.hits) > 0 && m.open != nil {
		out = append(out, result{kind: resHeader, label: "Nachrichten in " + m.open.Title()})
		for i, h := range m.hits {
			if i >= 8 {
				break
			}
			out = append(out, result{kind: resMsg, msg: h,
				label: sDim.Render(h.U.Username+": ") + oneLine(h.Msg)})
		}
	}

	m.results = out
	if m.rcursor >= len(out) || (len(out) > 0 && out[m.rcursor].kind == resHeader) {
		m.rcursor = 0
		if len(out) > 0 && out[0].kind == resHeader {
			m.moveResult(1)
		}
	}
}

type roomSource []Room

func (r roomSource) String(i int) string { return r[i].Title() + " " + r[i].Name }
func (r roomSource) Len() int            { return len(r) }

// --- view ---

func (m model) View() string {
	if m.w == 0 {
		return "…"
	}
	listW := 34
	if m.w < 100 {
		listW = max(22, m.w/3)
	}
	bodyH := max(3, m.h-2-m.footerH())

	dot := lipgloss.NewStyle().Foreground(cErr).Render("●")
	if m.connected {
		dot = lipgloss.NewStyle().Foreground(cOK).Render("●")
	}
	head := sTitle.Render("rc-tui") + "  " + dot + " " + sDim.Render(strings.TrimPrefix(m.cfg.URL, "https://"))

	var tabs []string
	for i, s := range sections {
		label := s.name
		if i == 0 {
			if n := countUnread(m.rooms); n > 0 {
				label = fmt.Sprintf("%s %d", s.name, n)
			}
		}
		if i == m.sec {
			tabs = append(tabs, sTabOn.Render(label))
		} else {
			tabs = append(tabs, sTab.Render(label))
		}
	}

	left := sDivider.Width(listW).Height(bodyH).Render(m.viewList(listW, bodyH))
	right := lipgloss.NewStyle().Width(m.vp.Width).Height(bodyH).Render(m.vp.View())
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)

	return clampFrame(lipgloss.JoinVertical(lipgloss.Left,
		head, lipgloss.JoinHorizontal(lipgloss.Bottom, tabs...), body, m.viewFooter()), m.w)
}

func (m model) viewList(w, h int) string {
	var rows []string
	if m.focus == focusSearch && !m.roomSearch {
		start := 0
		if m.rcursor >= h {
			start = m.rcursor - h + 1
		}
		for i := start; i < len(m.results) && len(rows) < h; i++ {
			r := m.results[i]
			if r.kind == resHeader {
				rows = append(rows, sGroup.Render(truncate(r.label, w-1)))
				continue
			}
			if i == m.rcursor {
				rows = append(rows, sSel.Width(w-1).Render(truncate("▸ "+stripStyle(r.label), w-1)))
			} else {
				rows = append(rows, truncate(" "+r.label, w-1))
			}
		}
		if len(m.results) == 0 {
			rows = append(rows, sDim.Render(" keine Treffer"))
		}
		return strings.Join(rows, "\n")
	}

	vis := m.visible()
	start := 0
	if m.cursor >= h {
		start = m.cursor - h + 1
	}
	for i := start; i < len(vis) && len(rows) < h; i++ {
		r := vis[i]
		// Die Zeile wird auf genau w-1 Spalten gebaut: sonst schiebt das Badge
		// hinter einem auf Breite gerenderten Cursor-Balken die Zeile um.
		count := ""
		if r.Unread > 0 {
			count = fmt.Sprintf(" %d", r.Unread)
		}
		width := w - 1
		name := truncate(r.Icon()+" "+r.Title(), width-1-lipgloss.Width(count))
		pad := strings.Repeat(" ", max(0, width-1-lipgloss.Width(name)-lipgloss.Width(count)))

		switch {
		case i == m.cursor && m.focus == focusList:
			rows = append(rows, sSel.Render(" "+name+pad+count))
		case i == m.cursor:
			rows = append(rows, lipgloss.NewStyle().Foreground(cAccent).Bold(true).Render(" "+name+pad+count))
		default:
			style := sBody
			if r.Type == "d" && !r.Multi() {
				style = userStyle(r.Name)
			}
			rows = append(rows, " "+style.Render(name)+pad+sUnread.Render(count))
		}
	}
	if len(vis) == 0 {
		rows = append(rows, sDim.Render(" leer"))
	}
	return strings.Join(rows, "\n")
}

// cellWidth ist lipgloss.Width plus dem Aufschlag, den Terminals für zusammengesetzte
// Emoji brauchen: runewidth rechnet eine Hautton- oder ZWJ-Sequenz als ein Doppelzeichen,
// viele Terminals malen aber jedes Teil-Emoji einzeln. Unterschätzen bricht die Zeile um
// und scrollt das Layout weg, überschätzen kostet nur Polsterung.
// ponytail: Stellknopf ist die 2 — Terminals, die Sequenzen korrekt zusammenlegen,
// bräuchten 0.
func cellWidth(s string) int {
	extra := 0
	for _, r := range s {
		if r == '\u200d' || (r >= '\U0001F3FB' && r <= '\U0001F3FF') { // ZWJ, Hautton-Modifier
			extra += 2
		}
	}
	return lipgloss.Width(s) + extra
}

var ansiTail = regexp.MustCompile(`(?:\x1b\[[0-9;]*m)+$`)

// clampFrame kürzt jede Zeile des fertigen Bildes auf w echte Spalten, indem es
// überzählige Polsterleerzeichen wegnimmt. Ohne das wird eine Zeile mit Emoji breiter
// als das Terminal, bricht um, das Bild wird eine Zeile höher als der Schirm — und die
// Oberfläche scrollt bei jedem Tastendruck nach oben weg.
func clampFrame(v string, w int) string {
	lines := strings.Split(v, "\n")
	for i, l := range lines {
		excess := cellWidth(stripStyle(l)) - w
		if excess <= 0 {
			continue
		}
		tail := ansiTail.FindString(l)
		body := strings.TrimSuffix(l, tail)
		for excess > 0 && strings.HasSuffix(body, " ") {
			body, excess = body[:len(body)-1], excess-1
		}
		// ponytail: bleibt Überhang, ist die Zeile echter Inhalt — nur ohne Escapes
		// kürzen, sonst zerschneidet man eine Sequenz.
		if excess > 0 && !strings.ContainsRune(body, 0x1b) {
			r := []rune(body)
			for excess > 0 && len(r) > 0 {
				excess -= cellWidth(string(r[len(r)-1]))
				r = r[:len(r)-1]
			}
			body = string(r)
		}
		lines[i] = body + tail
	}
	return strings.Join(lines, "\n")
}

// emojiChips rendert die Emoji-Auswahl als eine Zeile, die in budget Spalten passt.
func emojiChips(list []emojiEntry, cur, budget int) string {
	var cells []string
	used := 0
	for i, e := range list {
		cell := " " + e.Glyph + " " + e.Code + " "
		cost := cellWidth(cell)
		if used+cost > budget {
			break
		}
		used += cost
		if i == cur {
			cells = append(cells, sSel.Render(cell))
		} else {
			cells = append(cells, sDim.Render(cell))
		}
	}
	return strings.Join(cells, "")
}

func (m model) viewFooter() string {
	switch m.focus {
	case focusSearch:
		hint := "↑↓ oder ctrl+j/k wählen · enter öffnen · esc zurück"
		if m.roomSearch {
			hint = "↑↓ Treffer · enter springt hin · esc zurück in den Raum · from:me, order:asc gehen auch"
		}
		return m.ti.View() + "\n" + sDim.Render(hint)
	case focusCompose:
		hint := "enter senden · alt+enter neue Zeile · : Emoji · @ Person · esc abbrechen"
		if m.editing != nil {
			hint = "enter speichert die Änderung · alt+enter neue Zeile · esc verwirft sie"
		}
		if len(m.sugg) > 0 {
			tail := " tab übernimmt"
			hint = emojiChips(m.sugg, m.scurs, m.w-cellWidth(tail)) + sDim.Render(tail)
		}
		if m.dictStatus != "" {
			hint = sUnread.Render(m.dictStatus) + sDim.Render(" · ") + hint
		}
		// Beim Tippen steht der Cursor oft nicht unten — der Hinweis muss hierhin,
		// sonst sieht man neue Nachrichten genau dann nicht, wenn man schreibt.
		if m.below > 0 {
			hint = sUnread.Render(fmt.Sprintf("↓ %d neu", m.below)) + sDim.Render(" · ") + hint
		}
		return m.ta.View() + "\n" + hint
	case focusReact:
		prefix := ""
		if m.rfilter != "" {
			prefix = sBody.Render(":"+m.rfilter) + "  "
		}
		line := prefix + emojiChips(m.emojiChoices(), m.ecursor, m.w-cellWidth(stripStyle(prefix)))
		return line + "\n" + sDim.Render("tippen filtert · ←→ wählen · enter setzen/entfernen · esc zurück")
	}

	title := ""
	if m.open != nil {
		style := sTitle
		if m.focus == focusList {
			style = sDim // Fokus liegt links, rechts steht nur die Vorschau
		}
		title = style.Render(m.open.Icon() + " " + m.open.Title())
	}
	if m.thread != nil {
		title = sTitle.Render("↳ Thread") + sDim.Render(" in "+m.open.Icon()+m.open.Title())
	}
	if m.below > 0 {
		title += sUnread.Render(fmt.Sprintf("  ↓ %d neu", m.below)) + sDim.Render(" (G)")
	}
	if m.dictStatus != "" {
		title += sUnread.Render("  " + m.dictStatus)
	}
	status := m.status
	if status != "" {
		if m.statusErr {
			status = "  " + sErrTxt.Render(status)
		} else {
			status = "  " + sDim.Render(status)
		}
	}
	help := "j/k Räume · tab Section · enter/ctrl+w Chat · i schreiben · / suchen · r neu · q quit"
	if m.focus == focusMsgs {
		help = "j/k Nachricht · y kopieren · e bearbeiten · r reagieren · 1-9 abstimmen · t Thread · o öffnen · / suchen · ctrl+w Räume"
		if m.thread != nil {
			help = "j/k · e bearbeiten · r reagieren · o öffnen · i antworten · esc Raum · ctrl+w Unread"
		}
	}
	return title + status + "\n" + sDim.Render(help)
}

type renderOpts struct {
	width   int
	cursor  int
	me      string
	colored bool // Teilnehmerfarben (nur in Räumen mit mehr als zwei Personen)
	thread  bool // Thread-Antworten einschließen statt ausfiltern
}

// renderBlocks draws the UIKit blocks of an app message. Umfragen bringen ihre
// Balken schon als Text mit, die Optionen bekommen die Nummer ihrer Stimmtaste.
func renderBlocks(blocks []Block, msg string, w int) []string {
	var out []string
	n := 0
	for _, b := range blocks {
		switch b.Type {
		case "video_conf":
			out = append(out, sDim.Render("  📹 Videokonferenz"))
		case "section":
			t := strings.TrimSpace(b.Text.Text)
			if t == "" || t == strings.TrimSpace(msg) {
				continue // die Frage steht schon im Nachrichtentext
			}
			if b.Accessory.Type == "button" {
				n++
				out = append(out, sReaction.Render(fmt.Sprintf("  [%d] ", n))+sBody.Render(truncate(t, w-6)))
				continue
			}
			out = append(out, "  "+sBody.Render(truncate(t, w-2)))
		case "context":
			for _, e := range b.Elements {
				t := strings.TrimSpace(strings.ReplaceAll(e.Text, "`", ""))
				if t == "" {
					continue
				}
				out = append(out, sDim.Render("      "+truncate(t, w-6)))
			}
		}
	}
	return out
}

// renderMessages formats a history and records where each message starts.
func renderMessages(msgs []Message, o renderOpts) rendered {
	if len(msgs) == 0 {
		return rendered{content: sDim.Render("\n  keine Nachrichten")}
	}
	sort.SliceStable(msgs, func(i, j int) bool { return msgs[i].TS.Before(msgs[j].TS.Time) })
	out := rendered{}
	var lines []string
	lastAuthor, lastDay := "", ""

	for _, msg := range msgs {
		if msg.TMID != "" && !o.thread {
			continue // Thread-Antworten erscheinen nur in der Thread-Ansicht
		}
		var block []string
		day := msg.TS.Format("Mon, 02.01.2006")
		if day != lastDay {
			block = append(block, "", sDim.Render("── "+day+" ──"))
			lastDay, lastAuthor = day, ""
		}
		if msg.Msg == "" && msg.T != "" {
			block = append(block, sDim.Render("  · "+msg.T+" "+msg.U.Username))
			lastAuthor = ""
			out.starts = append(out.starts, len(lines))
			out.msgs = append(out.msgs, msg)
			lines = append(lines, block...)
			continue
		}
		if msg.Author() != lastAuthor {
			style := sAuthor
			if o.colored {
				style = userStyle(msg.U.Username)
			}
			if msg.U.Username == o.me {
				style = sMine
			}
			name := style.Render(msg.Author())
			block = append(block, "", name+" "+sDim.Render(msg.TS.Format("15:04")))
			lastAuthor = msg.Author()
		}
		block = append(block, strings.Split(renderText(msg.Msg, o.width), "\n")...)
		block = append(block, renderBlocks(msg.Blocks, msg.Msg, o.width)...)
		for _, a := range msg.Attachments {
			if a.Title != "" {
				block = append(block, sDim.Render("  📎 "+a.Title+"  (o öffnet)"))
			}
		}
		if len(msg.Reactions) > 0 {
			var rs []string
			for code, r := range msg.Reactions {
				rs = append(rs, fmt.Sprintf("%s %d", emoji.Sprint(code), len(r.Usernames)))
			}
			sort.Strings(rs)
			block = append(block, sReaction.Render("  "+strings.Join(rs, "  ")))
		}
		// Die Poll-App schreibt die Nachricht bei jeder Stimme neu -- das als
		// "bearbeitet" zu markieren wäre richtig, aber irreführend.
		if !msg.EditedAt.IsZero() && len(msg.Blocks) == 0 {
			block = append(block, sDim.Render("  (bearbeitet)"))
		}
		if msg.Replies > 0 {
			block = append(block, sDim.Render(fmt.Sprintf("  ↳ %d Antworten", msg.Replies)))
		}

		out.starts = append(out.starts, len(lines))
		out.msgs = append(out.msgs, msg)
		lines = append(lines, block...)
	}

	if o.cursor >= 0 && o.cursor < len(out.starts) {
		from := out.starts[o.cursor]
		to := len(lines)
		if o.cursor+1 < len(out.starts) {
			to = out.starts[o.cursor+1]
		}
		for i := from; i < to; i++ {
			if strings.TrimSpace(stripStyle(lines[i])) == "" {
				continue
			}
			lines[i] = sBar.Render("▎") + lines[i]
		}
	}
	out.content = strings.Join(lines, "\n")
	return out
}

// --- helpers ---

var (
	inTmux = os.Getenv("TMUX") != ""
	// tmuxPane adressiert unser eigenes Fenster — ohne -t würde tmux das gerade
	// aktive Fenster umbenennen, also womöglich ein fremdes.
	tmuxPane = os.Getenv("TMUX_PANE")
)

// tmuxArgv builds a tmux command line targeted at our own pane.
func tmuxArgv(sub string, tail ...string) []string {
	argv := []string{sub}
	if tmuxPane != "" {
		argv = append(argv, "-t", tmuxPane)
	}
	return append(argv, tail...)
}

// tmuxWindowName is the label shown in the tmux status bar.
func tmuxWindowName(n int) string {
	if n > 0 {
		return fmt.Sprintf("rocketchat ●%d", n)
	}
	return "rocketchat"
}

// tmuxBadge renames the tmux window so unread messages show up in the status bar.
func tmuxBadge(n int) tea.Cmd {
	if !inTmux {
		return nil
	}
	return func() tea.Msg {
		exec.Command("tmux", tmuxArgv("rename-window", tmuxWindowName(n))...).Run()
		return nil
	}
}

// tmuxRestore hands the window name back to tmux when we quit.
func tmuxRestore() {
	if !inTmux {
		return
	}
	exec.Command("tmux", tmuxArgv("set-window-option", "automatic-rename", "on")...).Run()
}

// badgeCmd reports the unread total to tmux, but only when it actually changed.
func (m *model) badgeCmd() tea.Cmd {
	n := 0
	for _, r := range m.rooms {
		n += r.Unread
	}
	if n == m.badge {
		return nil
	}
	m.badge = n
	return tmuxBadge(n)
}

func countUnread(rooms []Room) int {
	n := 0
	for _, r := range rooms {
		if r.Unread > 0 {
			n++
		}
	}
	return n
}

func hasRoom(rooms []Room, id string) bool {
	for _, r := range rooms {
		if r.ID == id {
			return true
		}
	}
	return false
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}

func stripStyle(s string) string {
	for {
		i := strings.IndexByte(s, 0x1b)
		if i < 0 {
			return s
		}
		j := strings.IndexByte(s[i:], 'm')
		if j < 0 {
			return s[:i]
		}
		s = s[:i] + s[i+j+1:]
	}
}

func truncate(s string, w int) string {
	if w < 1 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(stripStyle(s))
	if len(r) > w-1 {
		r = r[:max(0, w-1)]
	}
	return string(r) + "…"
}

func clamp(v, lo, hi int) int { return min(hi, max(lo, v)) }

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
