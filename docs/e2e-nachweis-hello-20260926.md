# E2E-Nachweis: Referenz-Plugin `hello` (mTLS + net/rpc)

**Datum:** 2026-09-26
**Plugin:** `hello` Version `0.1.2` (windows-amd64)
**Ergebnis:** bestanden — der Host startet das Out-of-Process-Plugin und erreicht es
erfolgreich über den Host-Contract-RPC.

## Gegenstand

Das Referenz-Plugin `hello` läuft als echter Out-of-Process-Prozess hinter einem
mTLS-Kanal und spricht mit dem Host über `net/rpc` (gob). Der Test belegt die
vollständige Kette Login → Source → Sync → Install → Review → Start → Readiness →
RPC-Ping gegen ein real gestartetes Plugin-Binary.

Der zuvor fehlerhafte Zustand: das Plugin registrierte seinen Dienst als
`Handshake`, der Host ruft jedoch exakt `HandshakeService.Ping` auf. `net/rpc`
matcht den Dienstnamen wörtlich, weshalb der Aufruf mit
`rpc: can't find service HandshakeService` scheiterte. Der Fix registriert den
Dienst unter dem vom Host erwarteten Wire-Namen:

```go
if err := server.RegisterName("HandshakeService", handshakeService{}); err != nil {
```

## Testaufbau

- Host: `glorious-platform-v2.exe` (Version `dev`), Datenverzeichnis
  `%TEMP%\gsc-comm-e2e\data`, API unter `http://127.0.0.1:8099`.
- Index-Server: lokaler HTTP-Server auf Port `8097`, der das signierte Archiv
  und die `index.json` ausliefert.
- Installationsquelle: das neu gebaute, signierte Archiv
  `hello-windows-amd64.glorious-plugin`
  (6.440.022 Bytes,
  sha256 `2ea6a48110ca14550d55a67f53bb52c601f0f91479a267f26deb8c85f13fed72`,
  `signer_pubkey sFzPAi3b5SsIRGhmwD8cboJqWrsj7TQAlh2cisSrlbY=`).
- Gegenprobe: eigenständiges Client-Binary (`rpcclient`), das sich ein
  kurzlebiges Client-Zertifikat aus der Plugin-CA ausstellt und anschließend
  `HandshakeService.Ping` über denselben mTLS-Kanal aufruft.

## Ablauf und Ergebnis

| # | Schritt | Ergebnis |
|---|---------|----------|
| 0 | `POST /api/v1/auth/login` (admin) | 200, Session + CSRF-Token |
| 1 | `POST /api/v1/marketplace/hello/uninstall` | sauberer Ausgangszustand |
| 2 | `POST /api/v1/marketplace/sources/{id}/sync` | 200, Index synchronisiert |
| 3 | `POST /api/v1/marketplace/install` (`hello-windows-amd64.glorious-plugin`) | **200, Version 0.1.2** — Signaturprüfung gegen `signer_pubkey` erfolgreich |
| 4 | `POST /api/v1/marketplace/plugins/hello/review` | 200, Review vermerkt |
| 5 | `POST /api/v1/marketplace/hello/start` | Plugin startet, wählt selbst Port `127.0.0.1:65468` |
| 6 | `GET /api/v1/marketplace` | Status `running`, Endpoint `127.0.0.1:65468`, Readiness ok |
| 7 | `rpcclient 127.0.0.1:65468` | **mTLS-Handshake + RPC-Ping erfolgreich, `EXIT=0`** |

### Rohausgabe der RPC-Gegenprobe (Schritt 7)

```
minted client cert CN="e2e-rpc-proof-client" serial=94385f53776c1cd1bf18dff3a708d1fb (signed by CA CN="glorious-platform plugin CA")
TLS established: version=TLS 1.3 cipher=TLS_AES_128_GCM_SHA256 peerCN="hello" peerSerial=d2fb95a52bddfcaf
HandshakeService.Ping -> Version="1.0"
EXIT=0
```

Bewertung der Rohausgabe:

- **mTLS aktiv:** Der Client stellt sich ein Zertifikat aus der Plugin-CA aus und
  wird vom Plugin akzeptiert (`tls.RequireAndVerifyClientCert` erfüllt).
- **TLS 1.3:** `version=TLS 1.3`, `cipher=TLS_AES_128_GCM_SHA256`.
- **Identität des Gegenübers:** Server-Zertifikat `peerCN="hello"`,
  `peerSerial=d2fb95a52bddfcaf` — bestätigt, dass tatsächlich das gestartete
  `hello`-Plugin antwortet, nicht ein Fremdprozess auf dem Port.
- **RPC-Vertrag erfüllt:** `HandshakeService.Ping` liefert `Version="1.0"` als
  wohlgeformte gob-Antwort. Vor dem RegisterName-Fix schlug genau dieser Aufruf
  fehl.

## Bewertung

Der Endpunkt lauscht nicht nur, sondern beantwortet den Host-Contract-RPC
korrekt. Damit ist belegt, dass der Referenzpfad „signiertes Community-Plugin
installieren, reviewen, starten und über mTLS per net/rpc ansprechen" durchgängig
funktioniert.

## Reproduzierbarkeit

Die SHA-256-Summe in `index.json` zeigt auf exakt dieses Testartefakt
(`2ea6a481…fed72`), sodass Installations- und Testpfad denselben Build betreffen.

## Dokumentationshinweis

Dieser Nachweis wurde KI-unterstützt erstellt (EU-AI-Act Art. 50). Alle Angaben
stammen aus der Rohausgabe des Testlaufs; es sind keine Geheimnisse (keine
privaten Schlüssel, keine Passwörter) enthalten.
