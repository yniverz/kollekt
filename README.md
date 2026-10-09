# Kollekt

Planungswerkzeug für Veranstaltungen von Kollektiven: Raves, Club-Nächte, Open Airs, kleine Feste. Pro Event laufen Genehmigungen, Location-Suche, Budget, Bar-Kalkulation, Line-up, Schichtplan, Material und Ablauf an einem Ort zusammen. Mehrere Personen arbeiten mit eigenen Logins und sehen nur, was für ihre Rolle gedacht ist.

Kollekt ist **kein Ticketsystem und keine Kasse**. Es dient der Planung und dem Überblick.

## Funktionen

| Bereich | Inhalt |
|---|---|
| **Events** | Anlegen aus Vorlage (Techno Open Air, Club Night, Kleines Oktoberfest, Leer), duplizieren, eigene Vorlagen aus bestehenden Events |
| **Module pro Event** | Jedes Modul lässt sich pro Event ein- und ausschalten |
| **Bereiche** | Bar, Technik, Einlass … mit Leitung, Kostenlimit und Live-Überblick (Aufgaben, Besetzung, Kosten) |
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

**Portainer:** Stacks → Add stack → *Repository* (dieses Repo, Compose-Pfad `docker-compose.yml`) oder den Inhalt der Datei in den *Web editor* kopieren. Bei privatem Repo ein GitHub-Token hinterlegen. Alternativ das von GitHub Actions gebaute Image `ghcr.io/yniverz/kollekt:latest` verwenden und in der Compose-Datei `build: .` durch `image: ghcr.io/yniverz/kollekt:latest` ersetzen.

| Variable | Standard | Bedeutung |
|---|---|---|
| `KOLLEKT_DATA` | `/data` | Ordner mit der SQLite-Datenbank (als Volume einbinden) |
| `KOLLEKT_ADDR` | `:8080` | Listen-Adresse |
| `KOLLEKT_SECURE_COOKIES` | leer | `1` erzwingt Secure-Cookies. Hinter HTTPS-Proxy (`X-Forwarded-Proto: https`) wird das automatisch erkannt |
| `KOLLEKT_ADMIN_USER` / `KOLLEKT_ADMIN_PASSWORD` | leer | Admin-Konto beim ersten Start anlegen (Passwort mindestens 10 Zeichen) |

**Backup:** Das Volume `/data` enthält alles (`kollekt.db`). Im laufenden Betrieb am besten mit `sqlite3 kollekt.db ".backup backup.db"` sichern oder den Container kurz stoppen.

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

## Hinweise zur Rechnung

* Beträge werden brutto erfasst. In der Kalkulation lässt sich ein Umsatzsteuersatz auf Einnahmen (Eintritt, Verkauf) einstellen, Vorsteuer wird nicht gerechnet.
* Pfand zählt nicht als Kosten. Er wird in der Einkaufsliste als Vorlage ausgewiesen.
* Kommissionsware („ungeöffnet zurückgebbar“) kostet nur den Verbrauch, alles andere ganze Gebinde inklusive Sicherheitspuffer.
* Break-even und Szenarien rechnen mit der Planungsbasis der Kalkulation. Ist-Werte (Beträge, Eintritt, verkaufte Portionen) ersetzen in der Prognose die Planwerte.
