# Kollekt

Planungswerkzeug für Veranstaltungen von Kollektiven: Raves, Club-Nächte, Open Airs, kleine Feste. Pro Event laufen Genehmigungen, Location-Suche, Budget, Bar-Kalkulation, Line-up, Schichtplan, Material und Ablauf an einem Ort zusammen. Mehrere Personen arbeiten mit eigenen Logins und sehen nur, was für ihre Rolle gedacht ist.

Kollekt ist **kein Ticketsystem und keine Kasse**. Es dient der Planung und dem Überblick.

## Funktionen

| Bereich | Inhalt |
|---|---|
| **Events** | Anlegen aus Vorlage (Techno Open Air, Club Night, Kleines Oktoberfest, Leer), duplizieren, eigene Vorlagen aus bestehenden Events |
| **Module pro Event** | Jedes Modul lässt sich pro Event ein- und ausschalten |
| **Bereiche** | Bar, Technik, Einlass … mit Leitung, Kostenlimit und Live-Überblick (Aufgaben, Besetzung, Kosten) |
| **Zeitplan** | Alle offenen Fristen und Termine (Aufgaben, Anträge, Zahlungen, Materialtermine) nach Kalenderwoche, mit „T−Tage“ bis zum Event, Filter „nur meine“ und Kalender-Abo (iCal) |
| **Aufgaben** | Bereich, Zuständigkeit, Frist, Priorität, Status direkt in der Liste änderbar |
| **Genehmigungen** | Behörde, Antragsfrist, Status, Aktenzeichen, Auflagen, Gebühr |
| **Location-Suche** | Locations anfragen, Angebote vergleichen, eine als gewählt festlegen |
| **Budget** | Einnahmen/Ausgaben, Plan und Ist, „Pro Gast“-Posten, Limits je Bereich. Kosten aus Line-up, Personal, Material, Genehmigungen, Location und Bar fließen automatisch ein |
| **Kalkulation** | Szenarien (z. B. 150/250/350 Gäste), Preisstufen, Break-even, Ergebnis nach Eintrittspreis, Kapazitätswarnung, optional Umsatzsteuer |
| **Bar & Verkauf** | Einkaufsartikel (Gebinde, Pfand, Schwund, Kommissionsware), Rezepte, Preise, Marge, Einkaufsliste pro Lieferant mit Sicherheitspuffer, Plan-/Ist-Auswertung |
| **Line-up** | Slots je Bühne mit Zeitleiste, Überschneidungswarnung, Gagen |
| **Personal** | Schichtplan mit offenen Plätzen, Stunden und Kosten pro Bereich/Person, Doppelbelegungen |
| **Ablaufplan** | Programm von Aufbau bis Abbau, Line-up wird automatisch eingeblendet |
| **Material** | Bedarf, Beschaffung (eigen/geliehen/gemietet), Status, Kosten |
| **Lageplan** | Wo steht was? Eigenen Plan (PNG/JPG/WebP) hochladen oder direkt auf einer OpenStreetMap-Karte planen. Punkte, Flächen, Rechtecke und Linien für Bühne, Bar, Einlass, Toiletten, Fluchtwege und Zäune, farbig nach Bereich, mit Flächen- und Längenberechnung. Jede Leitung zeichnet nur in ihrem Bereich, alle anderen sehen mit |
| **Karten & Pins** | Locations und Kontakte haben eine Position auf der Karte (Adresssuche oder Klick). Übersichtskarte aller Locations, Karte der Location-Anfragen und der Ort des Events in der Übersicht |
| **Strom** | Einspeisung → Verteiler → Verbraucher als Baum. Last je Verteiler mit Gleichzeitigkeit, Stromstärke und Auslastung gegen Absicherung bzw. Aggregat, Warnung bei Überlast, benötigte Einspeisung mit 20 % Reserve. Grobe Planungshilfe, die Auslegung macht eine Elektrofachkraft |
| **Checklisten** | Abnahme „Vor Einlass“, Aufbau, Abbau, Packlisten. Abhaken, pro Liste zurücksetzen, als Vorlage wiederverwendbar |
| **Transport** | Abholungen, Lieferungen, Team-Anfahrt. Strecke aus den Pins (Luftlinie × 1,3) oder manuell, Fahrtkosten fließen ins Budget, freie Plätze für Fahrgemeinschaften |
| **Nachbarschaft** | Wer wurde informiert, wer hat sich gemeldet? Anwohner-Brief mit Eventdaten zum Ausdrucken |
| **Nachbereitung** | Plan gegen Ist (Gäste, Einnahmen, Ausgaben, Bar), größte Abweichungen, Erkenntnisse mit Bewertung. Diese erscheinen bei künftigen Events als Erinnerung |
| **Event-Vergleich** | Alle Events nebeneinander (Gäste, Ergebnis, Marge, Werte pro Gast) und Einkaufspreise gleicher Artikel über Events hinweg |
| **Wetter** | In der Event-Übersicht: Vorhersage (bis 16 Tage vorher), sonst typisches Wetter der letzten 5 Jahre, mit Hinweisen zu Regen, Wind (Bühne/Zelte), Hitze und Kälte |
| **Anhänge** | An fast jedem Eintrag (Rechnung, Vertrag, Bescheid, Plan …): bis 25 MB pro Datei, höchstens 20 pro Eintrag, Büroklammer-Hinweis in der Liste |
| **Stammdaten** | Kontakte, Locations und Artikelstamm gelten für alle Events: Events werden aus diesen Bausteinen zusammengeklickt |

Die Übersicht jedes Events zeigt Kennzahlen, Warnungen (überfällige Aufgaben, verstrichene Antragsfristen, Verlust, Kapazität, offene Schichten, Artikel unter Einkaufspreis, Überschneidungen) und die nächsten Fristen.

## Rechte

* **Admin** sieht und ändert alles und verwaltet Benutzer, Rollen und Vorlagen.
* Jede Person bekommt **pro Event eine Rolle**. Eine Rolle legt je Modul fest: *Kein Zugriff*, *Lesen* oder *Bearbeiten*.
* **„Nur eigene Bereiche“**: Aufgaben, Budget, Personal und Material sind dann nur in den Bereichen sichtbar, für die die Person zuständig ist (z. B. Bar-Leitung sieht nur die Bar).
* Das Recht **„Kosten & Gagen sehen“** blendet Kostenfelder in Line-up, Personal, Material, Genehmigungen und Location aus.
* Mitgelieferte Rollen: Orga-Leitung, Finanzen, Bar-Leitung, Bereichsleitung, Booking, Helfer:in, Nur lesen. Alle sind bearbeitbar, eigene Rollen sind möglich.

## Betrieb mit Docker / Portainer

```bash
docker compose up -d --build
```

Danach `http://localhost:8080` öffnen. Beim ersten Aufruf erscheint die Einrichtung für das Admin-Konto. Alternativ legen die Variablen `KOLLEKT_ADMIN_USER` und `KOLLEKT_ADMIN_PASSWORD` das Konto beim ersten Start an.

**Portainer, Variante A (baut selbst, funktioniert sofort):** Stacks → Add stack → *Repository*, URL `https://github.com/yniverz/kollekt`, Reference `refs/heads/main`, Compose-Pfad `docker-compose.yml`. Das Repo ist öffentlich, ein Token ist nicht nötig. Portainer baut das Image dabei selbst. Zum Aktualisieren „Pull and redeploy“ nutzen und die Option *Re-pull image* ausgeschaltet lassen, es gibt nichts zu ziehen. Sonst meldet Docker „pull access denied for kollekt“.

**Variante B (fertiges Image, schneller):** GitHub Actions veröffentlicht `ghcr.io/yniverz/kollekt:latest` (amd64 und arm64). Damit Portainer es ziehen darf, einmalig das Paket auf GitHub öffentlich stellen (Profil → Packages → kollekt → *Package settings* → *Change visibility* → *Public*) und als Compose-Pfad `docker-compose.ghcr.yml` nehmen. Dort funktioniert *Re-pull image and redeploy*.

| Variable | Standard | Bedeutung |
|---|---|---|
| `KOLLEKT_PORT` | `8080` | Nur Compose: Port auf dem Host, unter dem Kollekt erreichbar ist (in Portainer als Environment-Variable des Stacks setzen). Im Container lauscht Kollekt immer auf 8080 |
| `KOLLEKT_DATA` | `/data` | Ordner mit der SQLite-Datenbank (als Volume einbinden) |
| `KOLLEKT_ADDR` | `:8080` | Listen-Adresse |
| `KOLLEKT_SECURE_COOKIES` | leer | `1` erzwingt Secure-Cookies (nur mit HTTPS!). Hinter HTTPS-Proxy (`X-Forwarded-Proto: https`) wird das automatisch erkannt. Bei reinem HTTP leer lassen, sonst klappt der Login nicht |
| `KOLLEKT_ADMIN_USER` / `KOLLEKT_ADMIN_PASSWORD` | leer | Admin-Konto beim ersten Start anlegen (Passwort 10 bis 72 Zeichen) |
| `KOLLEKT_TRUST_PROXY` | leer | `1` übernimmt die Client-IP aus `X-Forwarded-For` (nur hinter eigenem Proxy, sonst fälschbar) |
| `KOLLEKT_SOURCE_URL` | GitHub-Repo | Ziel des „Quellcode“-Links in der Fußzeile. Wer eine geänderte Version betreibt, muss hier auf den eigenen Quellcode verweisen (AGPL § 13) |
| `KOLLEKT_WEATHER` | an | `off` schaltet die Wetterdaten ab. Der Server fragt dann Open-Meteo nicht mehr ab |
| `KOLLEKT_MAPS` | an | `off` schaltet alle Funktionen mit externen Kartendiensten ab (Pins, Kartenpläne, Adresssuche). Bildpläne funktionieren weiter |
| `KOLLEKT_TILE_URL` | OpenStreetMap | Kachelserver, z. B. ein eigener oder ein Anbieter mit Vertrag. Platzhalter `{z}/{x}/{y}` und optional `{s}` |
| `KOLLEKT_TILE_ATTRIBUTION` | © OpenStreetMap-Mitwirkende | Quellenangabe, die im Kartenrand erscheint. Bei einem anderen Anbieter anpassen |
| `KOLLEKT_GEOCODER_URL` | Nominatim | Dienst für die Adresssuche |
| `KOLLEKT_DEMO_DATA` | leer | `1` legt in einer leeren Installation ein Demo-Event mit frei erfundenen Daten an |

**Backup:** Das Volume `/data` enthält alles (`kollekt.db` und der Ordner `files/` mit den Anhängen). Im laufenden Betrieb am besten mit `sqlite3 kollekt.db ".backup backup.db"` sichern oder den Container kurz stoppen.

**HTTPS:** Kollekt spricht selbst nur HTTP. Für Zugriff übers Internet einen Reverse Proxy (Caddy, Traefik, nginx Proxy Manager) davorsetzen.

## Entwicklung

```bash
go run ./cmd/kollekt          # Daten in ./data, Port 8080
go test ./...
```

Go 1.26, keine CGO-Abhängigkeit (SQLite über `modernc.org/sqlite`), Oberfläche per Go-Templates mit etwas Vanilla-JS, keine Build-Schritte für das Frontend.

### Aufbau

* `internal/app/modules.go` beschreibt alle Module deklarativ (Felder, Listenspalten, Filter, Gruppierung, Rechte). Ein neues Modul braucht dort nur einen Eintrag; Liste, Formular, Rechteprüfung und Vorlagen funktionieren generisch.
* `calc.go` und `bar.go` enthalten die Finanz- und Bar-Berechnung, `extras.go` die modulspezifischen Ansichten (Zeitleiste, Besetzung, Bereichskarten).
* `seed.go` enthält die mitgelieferten Rollen und Event-Vorlagen. Preise in den Bar-Vorlagen sind Beispielwerte.

## Richtwerte

Unter *Event-Einstellungen → Richtwerte* stellst du pro Event ein: ab wann Wetterwarnungen erscheinen (Regenwahrscheinlichkeit und -menge, Böen, Hitze, Kälte), Personen pro m² und Rettungswegbreite für den Dichte-Check (pro Plan überschreibbar), cos φ, „knapp“-Schwelle und Reserve beim Strom. Es sind Orientierungswerte ohne Rechtswirkung. Fehlerhafte Eingaben fallen auf die Startwerte zurück.

## Fristen und Kalender

* **Relative Fristen:** Bei Aufgaben, Genehmigungen und Budget-Posten kannst du „Frist relativ zum Event“ setzen (z. B. 42 Tage vorher, negativ = nach dem Event). Das Datum wird aus dem Eventtermin berechnet und wandert mit, wenn du den Termin änderst. Die mitgelieferten Vorlagen bringen Vorschläge für Vorlaufzeiten mit. Das sind grobe Richtwerte, keine amtlichen Fristen.
* **Kalender-Abo:** Auf der Zeitplan-Seite erzeugst du einen persönlichen `.ics`-Link. Er enthält Fristen (mit Erinnerung am Vortag um 9 Uhr), das Event, Line-up, Ablaufplan und deine eigenen Schichten, aber keine Beträge. Der Link wird nur einmal angezeigt, ist nur als Hash gespeichert und lässt sich jederzeit erneuern oder widerrufen. Er zeigt nur, was du in Kollekt sehen darfst, und wird ungültig, wenn du aus dem Event entfernt wirst.
* Das Dashboard zeigt Fristen der nächsten 14 Tage über alle deine Events.

## Netto und Brutto

* Pro Event stellst du unter *Einstellungen → Netto / Brutto* ein, in welcher Basis ausgewertet wird (Standard: **netto**), wie Kosten standardmäßig erfasst werden und welcher USt-Satz Standard ist (19 %).
* Einzelne Posten (Budget, Line-up, Material, Location, Einkaufsartikel) können abweichend **netto oder brutto** mit eigenem Satz (0 / 7 / 19 %) erfasst werden. Kleine Lieferanten ohne Umsatzsteuer: „Brutto“ mit 0 %. Alles wird in die Auswertungsbasis umgerechnet.
* Verkaufspreise (Bar) und Eintritt sind immer Brutto-Preise, so wie sie auf Karte und Ticket stehen. In der Netto-Auswertung wird die USt herausgerechnet.
* Pfand zählt nicht als Kosten. Er wird in der Einkaufsliste als Vorlage ausgewiesen. Vorsteuer, Zahllast und Steuererklärung sind nicht Teil von Kollekt.

## Vorlagen für Karlsruhe / Baden-Württemberg

Die mitgelieferten Genehmigungslisten orientieren sich an Karlsruhe und Baden-Württemberg (Ordnungs- und Bürgeramt, Landesgaststättengesetz, Fliegende Bauten, Sicherheitskonzept, GEMA, KSK …) und verlinken den [Veranstaltungsleitfaden der Stadt](https://web1.karlsruhe.de/service/Formulare/ordnungsamt/OA3_Veranstaltungsleitfaden_Karlsruhe.pdf). **Das sind Gedächtnisstützen ohne Gewähr, keine Rechtsberatung.** Fristen, Formulare und Zuständigkeiten bitte direkt bei der Stadt bestätigen. Preise in den Bar-Vorlagen sind Beispielwerte.

## Hinweise zur Rechnung

* Kommissionsware („ungeöffnet zurückgebbar“) kostet nur den Verbrauch, alles andere ganze Gebinde inklusive Sicherheitspuffer.
* Break-even und Szenarien rechnen mit der Planungsbasis der Kalkulation. Ist-Werte (Beträge, Eintritt, verkaufte Portionen) ersetzen in der Prognose die Planwerte.

## Sicherheit und Datenschutz

Siehe [SECURITY.md](SECURITY.md). Kurz: keine Zugangsdaten im Repository, Passwörter mit bcrypt, Sitzungen gehasht, CSRF-Schutz, serverseitige Rechteprüfung, strikte CSP, Anhänge nur als Download. Alle Beispieldaten (Vorlagen, Demo) sind frei erfunden.

## Karten und Datenschutz

Kartenkacheln und Adresssuche kommen standardmäßig von den öffentlichen OpenStreetMap-Servern (Kacheln: `tile.openstreetmap.org`, Suche: `nominatim.openstreetmap.org`). Dabei sieht OpenStreetMap die IP-Adresse und Anfragen der Nutzenden. Die öffentlichen Server sind für gelegentliche, kleine Nutzung gedacht (siehe deren Nutzungsrichtlinien). Für größere Installationen eigene Dienste über die Variablen oben eintragen. Mit `KOLLEKT_MAPS=off` gibt es keinerlei Verbindungen zu Kartendiensten. Die Wetterdaten ruft der Server (nicht der Browser) bei [Open-Meteo](https://open-meteo.com) ab und überträgt dabei nur die Koordinaten der Location und die Eventtage. Mit `KOLLEKT_WEATHER=off` ist das aus. Bildpläne im Lageplan brauchen nie einen externen Dienst. Die Content-Security-Policy erlaubt dem Browser nur die konfigurierten Kartenhosts.

Mitgeliefert: [Leaflet](https://leafletjs.com) 1.9.4 (BSD-2-Clause, Lizenztext unter `internal/app/web/static/vendor/leaflet/LICENSE`).

## Lizenz

Kollekt steht unter der [GNU Affero General Public License v3.0](LICENSE) (`AGPL-3.0-only`). Du darfst es nutzen, ändern und weitergeben. Wer es verändert und weitergibt oder als Webdienst für andere betreibt, muss den geänderten Quellcode unter derselben Lizenz bereitstellen. Für eine andere Lizenzierung bitte beim Urheber (yniverz) nachfragen.

Beiträge sind willkommen, siehe [CONTRIBUTING.md](CONTRIBUTING.md).
