# Sicherheit

Sicherheitslücken bitte **nicht** als öffentliches Issue melden, sondern über GitHub unter „Security“ → „Report a vulnerability“ (private Meldung).

## Was Kollekt tut

* Passwörter werden mit bcrypt gespeichert (mindestens 10 Zeichen), Sitzungs-Tokens nur als SHA-256-Hash.
* Sitzungs-Cookie `HttpOnly`, `SameSite=Lax`, `Secure` hinter HTTPS. Alle schreibenden Anfragen verlangen ein CSRF-Token und eine passende Herkunft.
* Anmeldeversuche sind begrenzt (pro Konto und IP).
* Jede Abfrage prüft Event-Mitgliedschaft, Modulrecht und Bereichszuordnung auf dem Server. Ausgeblendete Felder werden nicht ausgeliefert.
* Strikte Content-Security-Policy ohne Inline-Skripte, `X-Frame-Options: DENY`, `nosniff`.
* Kalender-Abo-Links sind zufällig (192 Bit), werden nur als Hash gespeichert, sind pro Person und Event widerrufbar und enthalten keine Beträge.
* Kartenfunktionen sprechen nur die konfigurierten Kartendienste an (CSP-Whitelist) und lassen sich mit `KOLLEKT_MAPS=off` komplett abschalten. Plan-Bilder werden nur ausgeliefert, wenn sie wirklich PNG, JPEG, GIF oder WebP sind (kein SVG), mit `nosniff` und `sandbox`-CSP.
* Anhänge liegen unter zufälligen Namen im Datenordner und werden nur als Download (`attachment`, `application/octet-stream`) ausgeliefert.
* Datenordner und Datenbank sind nur für den Prozessbenutzer lesbar. Der Container läuft ohne Root-Rechte.

## Betrieb

* Immer hinter einen HTTPS-Reverse-Proxy stellen und `KOLLEKT_TRUST_PROXY=1` nur setzen, wenn der Proxy `X-Forwarded-For` selbst setzt und Kollekt nicht direkt erreichbar ist.
* Keine Zugangsdaten ins Repository oder in `docker-compose.yml` schreiben. Admin-Zugang über die Einrichtungsseite oder Portainer-Umgebungsvariablen anlegen.
* Regelmäßig `/data` sichern (Datenbank und Ordner `files/`).
