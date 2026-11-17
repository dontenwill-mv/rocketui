# rc-tui

Rocket.Chat im Terminal — Bubble Tea / Lipgloss, im Stil von gh-dash.

## Konfiguration

`~/.config/rc-tui/config.yml`:

```yaml
url: https://rocketchat.example.com
user_id: <User-ID aus My Account > Personal Access Tokens>
token: <Personal Access Token>
insecure: false   # true nur bei selbst signiertem Zertifikat
```

Alternativ per Env: `RC_URL`, `RC_USER_ID`, `RC_TOKEN`, `RC_INSECURE=1`.

## Tastenbelegung

Zwei Fokus-Ebenen: Raumliste und Nachrichten. `enter` geht rein, `esc` wieder raus,
`ctrl+w` pendelt aus jeder Tiefe zwischen beiden. Welche Seite gerade dran ist, steht im
Raumtitel der Fußzeile: hell bei Fokus auf dem Chat, gedimmt bei Fokus auf der Liste.

Der Rückweg mit `ctrl+w` braucht den Raum nicht in der Liste — praktisch, wenn er nach
dem Lesen aus der Unread-Section gefallen ist.

Nach dem Senden einer Nachricht endet der Schreibmodus von selbst, `ctrl+k` und die
anderen globalen Tasten greifen also sofort wieder.

**Raumliste**

| Taste | Aktion |
|---|---|
| `j` / `k` | Raum wählen (Vorschau lädt automatisch) |
| `tab` / `shift+tab` | Section wechseln (Unread / Channels / DMs) |
| `enter` | in die Nachrichten wechseln |
| `i` | Nachricht schreiben |
| `/` | Suche |
| `r` | Raumliste neu laden |
| `q` | Beenden |

**Nachrichten**

| Taste | Aktion |
|---|---|
| `j` / `k` | Nachricht wählen (Marker links); `k` ganz oben lädt ältere nach |
| `g` / `G` | erste / letzte Nachricht |
| `pgup` | seitenweise scrollen, am oberen Rand lädt es ältere nach |
| `i` | Nachricht schreiben |
| `y` | Nachrichtentext in die Zwischenablage (`xclip`/`wl-copy`, sonst OSC 52) |
| `Y` | nur die Codeblöcke der Nachricht, ohne Zäune — mehrere durch Leerzeile getrennt |
| `e` | eigene Nachricht bearbeiten (`chat.update`) — `enter` speichert, `esc` verwirft |
| `r` | reagieren — tippen filtert, `←→` wählt, `enter` setzt oder entfernt |
| `t` | Thread der markierten Nachricht öffnen |
| `o` | Anhang oder Link der Nachricht extern öffnen (`xdg-open`) |
| `1`…`9` | in einer Umfrage für die Option mit dieser Nummer stimmen |
| `/` | Nachrichten **in diesem Raum** suchen |
| `esc` | zurück zur Raumliste (aus dem Thread: zurück in den Raum) |
| `ctrl+w` | pendelt: von überall in die Unread-Section der Raumliste, von dort zurück in den offenen Chat |
| `ctrl+k` | aus jeder Tiefe in die Raum- und Personensuche |
| `ctrl+d` | Diktat starten, nochmal: beenden und einfügen |

**Schreiben**

| Taste | Aktion |
|---|---|
| `enter` | senden |
| `alt+enter` | neue Zeile (Eingabefeld wächst bis zur halben Fensterhöhe) |
| `:` | Emoji-Vorschläge (am Wortanfang), `←→` wählt, `tab` übernimmt |
| `@` | Personen-Vorschläge, gleiche Bedienung — setzt `@username` ein |
| `ctrl+e` | Vorschläge manuell öffnen, setzt den `:` bei Bedarf selbst |
| `esc` | Vorschläge schließen, nochmal: abbrechen |

`e` legt den Text der markierten Nachricht ins Eingabefeld; fremde Nachrichten lehnt
der Server ohnehin ab, deshalb sagt rc-tui das gleich in der Statuszeile. Geänderte
Nachrichten tragen ein `(bearbeitet)`. Ob und wie lange Bearbeiten erlaubt ist, hängt an
`Message_AllowEditing` und `Message_AllowEditing_BlockEditInMinutes` deines Servers.

## Umfragen

Umfragen der Poll-App stecken nicht im Nachrichtentext, sondern in UIKit-`blocks`.
rc-tui zeichnet daraus Optionen, Balken und Wählerlisten — die Balken liefert die App
schon als Text, da ist nichts nachzurechnen:

```
📊 Arbeitet jemand mit den CLI-Commands?
  [1] Ich gebe selber CLI Commands ein
      ███                  16.67% (1)
      1 vote - vondergruen
  [2] Agent macht alles
      ████████████████     83.33% (5)
      5 votes - sanatgar sommer noethen loeffler kerneder
```

Die Ziffer links stimmt ab: `1` wählt die erste Option. Das geht über
`POST /api/apps/ui.interaction/<appId>` an die Apps-Engine, nicht über die normale
REST-API. Die App schreibt die Nachricht danach selbst um, DDP schiebt sie zurück, die
Balken aktualisieren sich von allein. Die Poll-App toggelt — dieselbe Ziffer nochmal
nimmt die Stimme wieder zurück.

Weil jede Stimme technisch eine Bearbeitung der Nachricht ist, bekommen Nachrichten mit
Blocks kein `(bearbeitet)`.

Im Thread schreibt `i` die Antwort direkt mit `tmid` in den Thread. Thread-Antworten
erscheinen nicht im Raumverlauf, dort steht nur `↳ n Antworten` an der Elternnachricht.

`@` schlägt vor, wer im offenen Raum geschrieben hat, Treffer am Anfang von Benutzer-
oder Anzeigename zuerst, dazu `@all` und `@here`. Der eigene Name fällt raus. Wer im
Raum nie etwas geschrieben hat, taucht nicht auf — dann hilft nur der Benutzername.

Kommt eine Nachricht an, während der Cursor weiter oben steht, scrollt rc-tui nicht
ungefragt weg. Stattdessen steht `↓ n neu` in der Fußzeile, auch während du schreibst;
`G` springt ans Ende und räumt den Zähler ab.

Nachrichtentext wird formatiert: Emoji-Shortcodes werden ersetzt, ` ``` `-Blöcke
bekommen Hintergrund und Sprachlabel, Inline-Backticks werden hervorgehoben.

In der Unread-Section bleiben gelesene Räume stehen, bis du die Section verlässt —
sonst rutscht die Liste unter dem Cursor weg, während man sie durchgeht.

Teilnehmernamen bekommen in Räumen mit mehr als zwei Personen eine stabile Farbe
(Hash über den Username); eigene Nachrichten bleiben grün.

## Suche

`/` sucht das, was in der jeweiligen Ebene Sinn ergibt. `ctrl+k` öffnet aus jedem Fokus
die Raum- und Personensuche — auch mitten aus einer Nachricht, einem Reaktions-Picker
oder einer laufenden Suche im Raum heraus. Nur in der Suche selbst bleibt `ctrl+k` der
Trefferzeiger.

**In der Raumliste: Räume und Personen.** Dreistufig, alles in einem Eingabefeld:

1. **Deine Räume** — lokales Fuzzy-Matching, ohne Request.
2. **Server** — `spotlight` für User und Channels außerhalb deiner Abos. `enter` auf einem User öffnet den DM.
3. **Nachrichten** — `chat.search` im offenen Raum, als Anhang an die Raumtreffer.

**In einem Raum: Nachrichten dieses Raums.** Kein Spotlight, keine Raumtreffer — die
Trefferliste steht im Nachrichtenbereich statt in der schmalen Seitenleiste, mit Autor,
Datum und hervorgehobenem Suchwort. `enter` lädt den Verlauf um den Treffer und springt
hin, `esc` geht zurück in den Raum.

Die Suchoperatoren des Servers gehen durch: `from:me`, `order:asc` und was dein
Rocket.Chat sonst kennt.

Treffer durchgehen mit `↑`/`↓` oder `ctrl+j`/`ctrl+k` (die Buchstaben selbst landen im
Suchtext), `enter` öffnet, `esc` zurück.

Rocket.Chat hat keine raumübergreifende Nachrichtensuche in der REST-API.

## Diktat

`ctrl+d` startet die Aufnahme, `ctrl+d` beendet sie; der Text landet an der Cursor-
position im Eingabefeld und wird nicht automatisch gesendet. Aufgenommen wird mit
`arecord` (16 kHz mono) über den Standard-Eingang von PipeWire — `RC_TUI_AUDIO_DEV`
setzt ein anderes ALSA-Gerät.

Transkribiert wird mit dem Helfer `rc-tui-stt`, der den WAV-Pfad als `$1` bekommt und
den Text nach stdout schreibt. Die mitgelieferte Fassung nutzt
[faster-whisper](https://github.com/SYSTRAN/faster-whisper) mit `large-v3-turbo` auf der
CPU — **alles lokal, keine API, keine Kosten**, die Aufnahme verlässt den Rechner nicht.

```bash
pip3 install --user faster-whisper
cp rc-tui-stt ~/.local/bin/   # oder einen eigenen Helfer gleichen Namens
```

Stellschrauben als Umgebungsvariablen: `RC_TUI_STT_MODEL` (Default `large-v3-turbo`),
`RC_TUI_STT_BEAM` (`5`), `RC_TUI_STT_LANG` (`de`). Auf 16 CPU-Kernen ohne GPU gemessen,
12 Sekunden Sprache:

| Modell | beam | laden | transkribieren |
|---|---|---|---|
| `large-v3-turbo` | 5 | 3,9 s | 7,7 s |
| `medium` | 1 | 1,9 s | 5,4 s |
| `small` | 1 | 0,7 s | 2,0 s |

`large-v3-turbo` trifft die Wortwahl am genauesten, `small` ist dreimal schneller und
verschluckt gelegentlich Füllwörter.

Warum kein Gedrückthalten wie anderswo: Terminals melden kein Key-Up. Sauber ginge das
nur über das Kitty-Keyboard-Protokoll, das bubbletea v1 nicht kann. Deshalb Umschalter.
`ctrl+d` löscht im Eingabefeld dadurch kein Zeichen mehr vorwärts — dafür bleibt `delete`.

## tmux

Läuft rc-tui in tmux (`$TMUX` gesetzt), benennt es das Fenster nach der Zahl der
ungelesenen Nachrichten: `rocketchat` ohne, `rocketchat ●7` mit Ungelesenen. Die Zahl
kommt live über DDP, der Bobble verschwindet also von selbst, sobald du gelesen hast.
Beim Beenden geht `automatic-rename` wieder an, das `rename-window` abschaltet.

## Realtime

DDP über `wss://…/websocket`: Login per Resume-Token, Subscriptions auf
`stream-room-messages` (`__my_messages__`) und `stream-notify-user`
(`subscriptions-changed`). Reconnect alle 3 s, Erstbefüllung über REST.

## Noch nicht drin

Siehe `TODO.md`.
