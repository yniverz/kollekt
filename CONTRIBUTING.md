# Mitmachen

Danke, dass du zu Kollekt beitragen willst.

## Ablauf

1. Issue eröffnen oder einen bestehenden Punkt kommentieren, bevor du größere Änderungen baust.
2. Branch anlegen, ändern, `go vet ./... && go test ./...` ausführen.
3. Pull Request mit kurzer Beschreibung stellen.

Sicherheitslücken bitte nicht als öffentliches Issue melden, sondern wie in [SECURITY.md](SECURITY.md) beschrieben.

## Lizenz deiner Beiträge

Kollekt steht unter `AGPL-3.0-only`. Mit deinem Pull Request erklärst du:

* Du hast das Recht, den Beitrag einzureichen, und er enthält keine fremden Inhalte ohne passende Lizenz.
* Dein Beitrag wird unter der `AGPL-3.0-only` veröffentlicht.
* Du räumst dem Projektinhaber (yniverz) zusätzlich ein unbefristetes, unwiderrufliches, weltweites, einfaches Recht ein, deinen Beitrag auch unter anderen Lizenzen zu vergeben, damit das Projekt bei Bedarf zusätzlich kommerziell lizenziert werden kann. Deine Urheberschaft bleibt bei dir.

Wenn du damit nicht einverstanden bist, reiche bitte keinen Beitrag ein.

## Stil

* Go-Code mit `gofmt`, keine neuen Abhängigkeiten ohne Grund.
* Neue Dateien beginnen mit `// Copyright (C) 2026 yniverz` und `// SPDX-License-Identifier: AGPL-3.0-only`.
* Oberfläche und Texte auf Deutsch, Beispieldaten immer frei erfunden.
