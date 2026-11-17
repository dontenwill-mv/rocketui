# TODO

Reihenfolge innerhalb einer Stufe ist verhandelbar, `[x]` = erledigt.

## Hoch

- [x] **Nachricht verfassen** — `chat.postMessage`, Eingabezeile im Footer via `i`.
- [x] **Mehrzeilige Nachrichten** — `bubbles/textarea`, `alt+enter` neue Zeile, `enter`
      sendet, Eingabefeld wächst bis 6 Zeilen und der Nachrichtenbereich schrumpft
      entsprechend. Offen: Dateianhänge senden.
- [x] **Emoji-Autocomplete im Text** — `:frag` am Textende öffnet die Vorschlagsleiste
      (`emoji.CodeMap()`, Prefix-Treffer vor Substring-Treffern), `tab` oder `enter`
      setzt das Unicode-Zeichen ein. Der Reaktions-Picker nutzt denselben Katalog, die
      20er-Liste ist nur noch der Vorschlag bei leerem Filter. Funktioniert an jeder
      Cursorposition, `ctrl+e` öffnet die Leiste manuell und setzt den Doppelpunkt bei
      Bedarf selbst.
- [x] **Auf Nachricht reagieren** — `chat.react` mit Nachrichten-Cursor (`enter` in der
      Raumliste → `j`/`k` → `e`) und Picker aus 20 Reaktionen plus freier `:shortcode:`-Eingabe.
      Toggle erkennt eigene Reaktionen über `/api/v1/me`.

## Mittel

- [x] **Nachricht kopieren** — `y` legt den Rohtext der markierten Nachricht in die
      Zwischenablage, `Y` nur die Codeblöcke (ohne ```-Zäune, mehrere durch Leerzeile
      getrennt). Weg: erst `wl-copy`/`xclip`/`xsel`, sonst OSC 52 — das reicht tmux mit
      `set-clipboard on|external` nach außen durch und trägt auch über SSH. Löst das
      Markieren per Maus ab, das im Vollbild-Layout immer die Raumliste mitnimmt.

- [x] **Direkt zur Raumliste springen** — `ctrl+w` springt aus jeder Tiefe in die linke
      Leiste, verwirft den Thread-Zustand und schließt Eingabe und Vorschläge.

- [x] **Emoji-Rendering** — `kyokomi/emoji` ersetzt Shortcodes in Nachrichtentext und
      Reaktionsleiste. Innerhalb von Code-Blöcken bleibt alles unangetastet. Server-eigene
      Custom-Emojis haben kein Unicode-Äquivalent und bleiben als Shortcode stehen —
      die brauchen den Bild-Punkt.
- [x] **Code-Blöcke schön rendern** — Fences werden als Block mit Hintergrund und
      Akzentbalken dargestellt, Sprache als Label, Inline-Backticks hervorgehoben.
      Offen: Syntax-Highlighting per `chroma`.
- [x] **Unterschiedliche Farben für Usernamen** — `fnv`-Hash auf eine Palette von 10
      Farben, nur in Räumen mit mehr als zwei Personen (`uids` unterscheidet 1:1-DMs von
      Gruppen-DMs). DM-Namen in der Seitenleiste tragen die Farbe des Gegenübers.

- [x] **Ältere Nachrichten nachladen** — `k` auf der obersten Nachricht (oder `pgup` am
      oberen Rand) holt die nächste Seite vor der ältesten bekannten Nachricht. Seiten
      ohne sichtbare Nachricht (reine Thread-Antworten) werden übersprungen, maximal 5
      am Stück. Zuvor war der Verlauf bei 80 Nachrichten zu Ende — und in Räumen, in
      denen DDP schon live etwas geliefert hatte, wurde er gar nicht erst geholt.

- [x] **Suche im Raum** — `/` ist jetzt kontextabhängig: in der Raumliste Räume und
      Personen, im Raum nur die Nachrichten dieses Raums. Treffer (50 statt 15) stehen
      im Nachrichtenbereich mit Autor, Datum und hervorgehobenem Suchwort.
      Offen: `n`/`N` zum Durchsteppen ohne die Suche erneut zu öffnen; Thread-Treffer
      ankern auf der Elternnachricht statt die Thread-Ansicht zu öffnen.

- [x] **Eigene Nachrichten bearbeiten** — `e` auf einer eigenen Nachricht öffnet das
      Compose-Feld mit ihrem Text, `enter` schickt `chat.update`, `esc` verwirft. Das
      Reagieren ist dafür auf `r` gewandert. Bearbeitete Nachrichten zeigen
      `(bearbeitet)` über `editedAt`. Offen: Löschen (`chat.delete`).

- [x] **Umfragen anzeigen und abstimmen** — `blocks` (UIKit) werden gerendert: `section`
      als Option mit Nummer, `context` als Balken und Wählerliste, `video_conf` als
      Hinweiszeile (vorher waren Videocall-Nachrichten komplett leer). `1`-`9` stimmt über
      `/api/apps/ui.interaction/<appId>` ab. Offen: „Finish poll" (die Overflow-Aktion),
      Umfragen selbst anlegen — der `/poll`-Befehl der App öffnet einen Modal-Dialog, den
      `commands.run` über REST nicht zu Ende bringt.

- [x] **Globale Raumsuche auf `ctrl+k`** — öffnet die Raum- und Personensuche aus jedem
      Fokus. In der Suche selbst bleibt `ctrl+k` der Trefferzeiger.

- [x] **Mentions mit Vorschlägen** — `@frag` im Text öffnet dieselbe Leiste wie `:frag`,
      Kandidaten sind die Schreibenden des offenen Raums plus `@all`/`@here`.
      Offen: `channels.members`/`spotlight` für Leute, die im Raum nie geschrieben haben.
- [x] **Hinweis auf neue Nachrichten** — steht der Cursor nicht am Ende, zeigt die
      Fußzeile `↓ n neu` statt wegzuscrollen. Kein Bug im Live-Pfad: DDP liefert auch
      Thread-Antworten, nur gesehen hat man sie nicht.

- [x] **Diktat per `ctrl+d`** — `arecord` nimmt auf, der Helfer `rc-tui-stt`
      (faster-whisper, lokal, CPU) transkribiert, der Text landet am Cursor im
      Eingabefeld. Gedrückthalten geht nicht: Terminals melden kein Key-Up, und das
      Kitty-Keyboard-Protokoll kann bubbletea v1 nicht — deshalb Umschalter.
      Offen: Streaming-Transkription während der Aufnahme statt Warten danach.
- [x] **Eingabefeld wächst mit dem Fenster** — statt fest bei 6 Zeilen jetzt bis zur
      halben Fensterhöhe, Untergrenze 6. Ein mehrzeiliges Diktat war sonst nicht
      vollständig zu sehen. Ein Resize zieht die Höhe nach.

- [x] **Fokus-Handling** — Senden beendet den Schreibmodus (vorher klebte man darin und
      tippte globale Tasten in den nächsten Entwurf). `ctrl+w` pendelt jetzt zwischen
      Raumliste und offenem Chat, statt nur nach links zu springen; der Rückweg findet
      den Raum auch, wenn er aus der Unread-Section gefallen ist. Der Raumtitel in der
      Fußzeile ist gedimmt, solange der Fokus links liegt.

## Nice to have

- [x] **tmux-Bobble** — bei Ungelesenen heißt das tmux-Fenster `rocketchat ●N`, sonst
      `rocketchat`; gemeldet wird nur bei echter Änderung, beim Beenden wird
      `automatic-rename` zurückgesetzt. Offen: `@rc_unread` als User-Option für eine
      eigene Statusleisten-Formatierung.

- [x] **Links und Anhänge öffnen** — `o` auf der markierten Nachricht: Anhänge werden mit
      Auth-Header in eine Tempdatei geladen und per `xdg-open` im passenden Programm
      geöffnet, reine URLs gehen direkt an `xdg-open`. Nur Linux.

- [x] **Suche schaltet Section mit** — beim Öffnen aus der Suche wechselt die linke Leiste
      in die passende Section und setzt den Cursor auf den Raum.
- [ ] **Bilder inline anzeigen** (öffnen geht bereits per `o`) — braucht ein Terminal-Grafikprotokoll: Kitty, iTerm2 oder
      Sixel. Erkennung über `$TERM`/`$KITTY_WINDOW_ID`, Fallback ANSI-Blöcke (chafa-Logik)
      oder `o` = im externen Viewer öffnen. **Offene Frage: welches Terminal nutzt du?**
      Davon hängt ab, ob das billig oder aufwendig wird.
- [ ] **Profilbilder** — `/avatar/<username>` liefert PNG. Gleiche Terminal-Abhängigkeit
      wie Bilder; ohne Grafikprotokoll bleibt nur ein farbiges Initialen-Badge.
- [x] **Thread-Ansicht** — `t` auf einer Nachricht öffnet den Thread (`chat.getThreadMessages`),
      `i` antwortet mit `tmid`, `esc` geht zurück in den Raum. Offen: `chat.getThreadsList`
      als Übersicht aller Threads eines Raums.
- [x] **Sprung zum Suchtreffer** — `enter` auf einem Nachrichtentreffer lädt das Fenster
      vor der Nachricht (`latest`) und setzt den Cursor darauf; Thread-Antworten ankern
      auf ihrer Elternnachricht. Offen: Lücke zwischen Treffer-Fenster und aktuellem Ende
      ist nur am Datumstrenner erkennbar.
- [ ] **Syntax-Highlighting in Code-Blöcken** — `chroma`, Sprache steht am Fence.
- [ ] **Custom-Emojis des Servers** — `emoji-custom.list` liefert Name plus Bild-URL;
      ohne Grafikprotokoll bleibt nur der Shortcode.
- [ ] **Raumübergreifende Nachrichtensuche** — REST kann das nicht; nur durch Iteration
      von `chat.search` über N Räume. Langsam, viele Requests.
