[English](README_EN.md) | [简体中文](README.md)

# ADBreeze

**A featherweight cross-platform ADB control center** — a single binary that runs on Windows / macOS / Linux / Docker / Android and gives you full control of any Android device from your browser: screen mirroring, reverse touch control, terminal, file manager, clipboard, screenshots, APK installation.

> Only ADB runs locally; every feature UI lives on the web.

Project home: <https://github.com/smanx/adbreeze>

Project intro page (online overview): <https://smanx.github.io/adbreeze/#/about>

## Features

- **Live screen mirroring**: based on the scrcpy protocol (H.264), hardware-decoded in the browser via WebCodecs with ~100ms latency; one stream supports multiple viewers
- **Audio forwarding**: device audio (Opus) forwarded in real time, decoded via WebCodecs and played through an AudioWorklet; mutable
- **Reverse control**: mouse click/drag/scroll and PC keyboard drive the device directly; rotation-adaptive (the server parses SPS to track resolution in real time)
- **Clipboard sync**: read/write the device clipboard, one-click copy to your machine
- **Interactive terminal**: native PTY shell; the xterm.js frontend fully supports interactive programs like vim/top
- **File manager**: directory browsing, upload/download, delete, create (native adb sync protocol implementation)
- **App management**: app list, launch / stop / uninstall, export APK
- **Device info / performance monitoring**: system overview, live CPU / memory charts
- **Process management**: process list with one-click kill
- **Logcat**: view device logs in real time from the browser
- **Screenshot / APK install**: one-click screencap PNG download; drag-and-drop APK install from the browser
- **Batch commands**: select multiple devices and run shell commands in parallel
- **Device hot-plug**: a persistent `host:track-devices` connection pushes device connect/disconnect events in real time
- **Single-file delivery**: the frontend and scrcpy-server are both embedded in the binary — deployment = copying one file (~10MB)
- **adb server self-healing**: automatically restarts the adb server if it disappears
- **Public internet access (optional)**: enable Cloudflare Tunnel with one click in Settings and the service instantly gets a public HTTPS address — no public IP, no router port forwarding; cloudflared is not bundled (one-click install in Settings keeps the package size unchanged)

## Architecture

```
Browser (Web UI)
   │  REST (control) + WebSocket (video / terminal / events)
   ▼
ADBreeze service process (Go single binary)
   ├── ADB layer ──── adb server (5037, native smart socket protocol)
   ├── scrcpy layer ── on-device scrcpy-server.jar (sync push → tunnel → client protocol parsing)
   └── Devices: USB / adb connect over network / the host itself
```

- **ADB layer**: no dependency on the `adb` subprocess (except for starting the server) — native implementation of smart socket (`host:transport` / `host:track-devices` / forward / reverse) and the sync protocol (SEND/DATA/DONE, LIST/RECV)
- **Mirroring**: embeds `scrcpy-server` v5.0.1, reverse tunnel first (no connection race) with automatic forward fallback; the video stream fans out through a Hub to multiple WebSocket subscribers, and new subscribers automatically receive SPS/PPS aligned to a keyframe
- **Authentication**: admin password login (in-page login form); after login, a 12-hour session (cookie / Bearer token) grants access, with logout and brute-force lockout

## Quick Start

**Direct download**: no build needed — grab the single-file build for your platform from [GitHub Releases](https://github.com/smanx/adbreeze/releases) (on Windows, just double-click, see below), or pull the prebuilt Docker image (see the [Docker](#docker) section).

**Build from source**: the frontend build output `web/dist` is embedded into the binary via `go:embed` and is not distributed with the repo (see `.gitignore`), so **the first build after cloning requires building the frontend first** (dependencies managed with [pnpm](https://pnpm.io)):

```powershell
# 1. Build the frontend (Node 18+ / pnpm 10+)
cd web
pnpm install
pnpm run build
cd ..

# 2. Build (Go 1.26+; or `make build`, which builds the frontend first automatically)
go build -o bin/adbreeze.exe ./cmd/adbreeze

# 3. Run: every parameter is optional; default password admin, HTTP :9080 + HTTPS :9443
.\bin\adbreeze.exe

# Override when needed (precedence: CLI flags > environment variables > defaults)
.\bin\adbreeze.exe -addr :8000 -https=false -password my-secret
```

**On Windows, double-click `adbreeze-windows-amd64.exe` and it just runs**: launching with no arguments = default config; once the service is up, the default browser opens the admin UI automatically, and the console window keeps running logs (closing the window stops the service). Running from a console, or with arguments, is also supported — launching with arguments counts as a "console / service scenario" and won't auto-open the browser (add `-open-browser` explicitly if you want it).

> If SmartScreen blocks the first launch ("Windows protected your PC"), click "More info → Run anyway"; every unsigned open-source app gets this prompt. If startup fails, the window stays open with the reason and exits on Enter — it never flashes and vanishes.

Open `http://localhost:9080` in your browser (HTTPS at `https://localhost:9443`) and enter the admin password in the login box.

### Configuration (CLI flags / environment variables / defaults)

The three sources rank from high to low: **CLI flags > environment variables > defaults**. Everything is optional.

| Setting | Flag | Env var | Default | Description |
|---|---|---|---|---|
| Admin password | `-password` | `ADBREEZE_PASSWORD` | `admin` | Used for the web login; `-user` is deprecated (kept only for old scripts, no effect) |
| HTTP port | `-addr` | `ADBREEZE_ADDR` | `:9080` | HTTP is always on; a bare port number (e.g. `9080`) also works |
| HTTPS switch | `-https` | `ADBREEZE_HTTPS` | `true` | `-https=false` disables it (HTTP always available) |
| HTTPS port | `-https-addr` | `ADBREEZE_HTTPS_ADDR` | `:9443` | Must differ from the HTTP port |
| adb path | `-adb` | `ADBREEZE_ADB` | PATH lookup | When PATH fails, common install locations are probed automatically (see below); can also be switched anytime in the web Settings |
| Auto-open browser | `-open-browser` | `ADBREEZE_OPEN_BROWSER` | see description | On by default for argument-less launches (double-click / Finder); off for launches with arguments / on Linux |
| Data directory | `-data` | `ADBREEZE_DATA` | `ADBreeze` under the user config dir | Stores TLS certificates; must persist |
| TLS certificate | `-tls-cert` / `-tls-key` | `ADBREEZE_TLS_CERT` / `ADBREEZE_TLS_KEY` | auto-generated | Provide a real certificate as a pair |
| Extra certificate hostnames | `-tls-hosts` | `ADBREEZE_TLS_HOSTS` | empty | Comma-separated; extra domains added to the auto-certificate SAN |
| Extra certificate IPs | `-tls-ips` | `ADBREEZE_TLS_IPS` | empty | Comma-separated; extra IPs added to the auto-certificate SAN |
| Public tunnel switch | `-tunnel` | `ADBREEZE_TUNNEL` | `false` | Enables Cloudflare Tunnel at startup (for headless scenarios; full config lives in Settings) |
| Public tunnel token | `-tunnel-token` | `ADBREEZE_TUNNEL_TOKEN` | empty | Named-mode token; empty = Quick Tunnel with a random URL |
| WebRTC STUN | `-webrtc-stun` | `ADBREEZE_WEBRTC_STUN` | two public STUNs | Comma-separated; `off` disables; override with your own coturn / TURN |

```bash
# Environment variable example (containers / systemd, etc.)
ADBREEZE_PASSWORD=my-secret ADBREEZE_ADDR=9080 ./adbreeze
```

### Auto-open browser (double-click launch)

`-open-browser` is on by default but **only takes effect automatically for argument-less launches** — exactly the Windows double-click / macOS Finder scenario: the user is right at the computer, so once the service is ready the admin UI is brought straight to the default browser instead of making them guess the URL from a console. Rules:

| Launch method | Auto-opens browser |
|---|---|
| Double-click exe / Finder launch (no arguments) | Yes |
| Console / server launch with arguments (`-addr` etc.) | No (add `-open-browser` explicitly if needed) |
| Linux (incl. Docker / Termux) | No (no desktop browser; only tries `xdg-open` when explicitly enabled) |
| Explicit `-open-browser=false` or `ADBREEZE_OPEN_BROWSER=false` | Never |

Auto-opening the browser is a convenience fallback: failure is only logged and doesn't affect the service — just visit the address printed in the startup log. On Windows, startup also switches the console code page to UTF-8 (Chinese logs stay readable) and restores it on exit, without affecting anything you run afterward in the same cmd window.

### adb auto-detection

When `-adb` is empty, adb is located in this order: **PATH → common install locations**. Common locations cover `ANDROID_HOME` / `ANDROID_SDK_ROOT`, a `platform-tools` directory next to the executable, and the standard per-platform SDK directories (Windows: `%LOCALAPPDATA%\Android\Sdk\platform-tools` etc.; macOS: `~/Library/Android/sdk/platform-tools`; Linux: `~/Android/Sdk/platform-tools`). Behavior is identical to before when PATH already resolves adb; probing only kicks in when PATH fails — on Windows adb is rarely on PATH, and without this step a double-click launch would show no devices at all. The log prints the path actually in use; to point elsewhere, use `-adb` or switch in the web "Settings" page (takes effect immediately, no restart).

### HTTPS (on by default)

WebCodecs (`VideoDecoder` / `AudioDecoder`) are only exposed in **secure contexts**, i.e. HTTPS or `localhost`. When you visit via a LAN IP like `http://192.168.x.x:9080`, the browser doesn't offer these APIs: video automatically falls back to WebRTC or WASM software decoding (audio likewise falls back to libopus WASM decoding, no extra configuration). With HTTPS enabled, those visits get WebCodecs hardware decoding too.

```powershell
.\bin\adbreeze.exe                                # HTTP :9080 + HTTPS :9443 both on (default)
.\bin\adbreeze.exe -https=false                   # HTTP :9080 only
.\bin\adbreeze.exe -https-addr :9444              # custom HTTPS port

# Use your own real certificate
.\bin\adbreeze.exe -tls-cert cert.pem -tls-key key.pem
```

**HTTP and HTTPS coexist; it's not either/or**: HTTP always listens as the guaranteed entry — before the browser trusts the self-signed certificate it's the only way in, and if HTTPS can't start you don't lose access entirely. Both share the same routes and authentication, so **they must use different ports** (same port is rejected at startup with a message).

#### Trusting the CA is a one-time step (immune to IP / hostname changes)

When `-tls-cert` / `-tls-key` are not given, a **local CA + leaf certificate** two-tier structure is generated automatically, stored under `tls/` in the **data directory** (default: `ADBreeze` under the user config dir; change with `-data` / `ADBREEZE_DATA`):

| File | Purpose | Does it change? |
|---|---|---|
| `ca.pem` | Trust anchor: **this is what you import into the system trust store** | Never (stable fingerprint) |
| `ca.key` | CA private key, used to sign leaf certificates | Never |
| `cert.pem` / `key.pem` | Leaf certificate: SAN covers the current hostname and all NIC IPs | **Auto-re-signed** when the name set changes |

The key point is that **the trust anchor is the CA**, so when the host's hostname or IP changes (new network, new machine name), only the leaf certificate is re-signed — the CA fingerprint is unchanged and **the trust you imported keeps working, no re-import needed**. (With a single self-signed certificate, every name change means re-issuing, a changed fingerprint, and voided trust — you'd have to re-import every time the environment changes.)

The data directory must **persist**: once the CA files are lost (e.g. a system cleaner wiped the temp dir, or the container was recreated), the next startup generates a brand-new CA and all imported trust is void. That's why the default data directory lives in the user config dir and the Docker image writes to the `/config` volume by default. When upgrading from older versions, the first launch automatically migrates old certificates from the temp directory to the new location (same fingerprint, no re-import).

The startup log prints the hostnames and IPs covered by the certificate plus the path to `ca.pem`. For first-time setup (choose one):

1. **Download & install from the UI (recommended)**: in the web "Settings → HTTPS Certificate" page, download the CA certificate (`.crt`) or the iOS profile (`.mobileconfig`); the page includes step-by-step install instructions for each platform (Windows / macOS / Linux / Android / iOS) and the CA fingerprint for verification
2. **Manual import**: import the data directory's `tls/ca.pem` into the system trust store (Windows: `certmgr` → Trusted Root Certification Authorities → Import; requires admin rights), then restart the browser

Install once and you're done: the CA is long-lived, and later network changes, IP changes, or service restarts never require reinstalling. On iOS/iPadOS, note that after installing the profile you must also enable full trust under "Settings → General → About → Certificate Trust Settings" — both steps are required.

Both the CA and leaf certificates are valid for 10 years; no rotation needed. To fully reset, delete the `tls/` directory and restart (a brand-new CA is generated, and you'll need to re-import trust once).

> On Windows, `curl` (schannel) reports `revocation status unknown` because the CA carries no revocation info; this doesn't affect browsers — Chrome / Edge / Firefox use their own validation logic and treat the CA as trusted once imported.

#### Adding hostnames / IPs to the certificate (environment variables)

Auto-detection only covers **this machine's NIC IPs and hostname**. In the scenarios below, the name you use to visit isn't detectable, so add it to the certificate SAN explicitly via environment variables (comma-separated, **unioned** with auto-detected names; localhost and other entrypoints are always kept):

| Scenario | Configuration |
|---|---|
| Custom domain / reverse-proxy domain | `ADBREEZE_TLS_HOSTS=adb.example.com` |
| External address behind a Docker bridge / NAT | `ADBREEZE_TLS_IPS=203.0.113.10` |

```bash
# Domain + external IP together (CLI equivalent: -tls-hosts adb.example.com -tls-ips 203.0.113.10)
ADBREEZE_TLS_HOSTS=adb.example.com ADBREEZE_TLS_IPS=203.0.113.10 ./adbreeze
```

Behavior notes:

- Added names enjoy the same mechanism as auto-detected ones — **trust the CA once, valid forever**: name-set changes only re-sign the leaf certificate; the CA fingerprint is unchanged and no re-import is needed
- Invalid IPs are logged and ignored without affecting startup; a hostname written in IP form (e.g. `ADBREEZE_TLS_HOSTS=192.168.1.5`) is automatically treated as an IP
- Only effective when certificates are **auto-generated**; when `-tls-cert` is given, that certificate's own SAN applies

### Public internet access (Cloudflare Tunnel)

When you have no public IP and don't want port forwarding, Cloudflare Tunnel can publish the service to the internet: cloudflared only **makes outbound connections proactively** and never listens for inbound traffic, so it works behind home broadband or corporate NAT. Full design and security rationale in [`CF-TUNNEL-DESIGN.md`](CF-TUNNEL-DESIGN.md).

**cloudflared is not bundled** (+0 to package size). Two install options, pick either:

1. **One-click install (recommended)**: Settings → Public Access → click "Install" — downloads the binary for the current platform (the server's `GOOS/GOARCH`, which may differ from your browser's platform) from Cloudflare's official GitHub releases into the data directory's `bin/`; it's detected automatically after install with no restart, and you can re-run it anytime to update. The UI shows the version and SHA256 for verification
2. **Manual install**:

| Platform | Install |
|---|---|
| Windows | `winget install --id Cloudflare.cloudflared` |
| macOS | `brew install cloudflared` |
| Linux | deb / rpm / binary from the official release page |

Then in **Settings → Public Access (Cloudflare Tunnel)**, confirm cloudflared is ready (path and version shown) and click "Enable" to get a `https://<random-words>.trycloudflare.com` URL (Quick Tunnel, no Cloudflare account needed). The URL is random and unguessable if unshared; Cloudflare's edge provides a trusted certificate, so the browser side is a secure context and **the local CA doesn't need to be trusted**. The one-click install and winget / brew share the same source (pinned official repo + HTTPS; Cloudflare doesn't publish checksum files with releases, so no external checksum comparison — instead, a post-install `--version` smoke test plus SHA256 display).

Two modes:

| | Quick Tunnel (default) | Named Tunnel |
|---|---|---|
| Account | Not needed | Required (free) |
| URL | Random, changes on restart | Fixed domain (bound in the CF dashboard) |
| Frontline protection | None | Can add Cloudflare Access (email OTP / Google sign-in) |

Named mode: paste the tunnel token from the CF dashboard into the Settings page. **We recommend adding Cloudflare Access to the fixed domain** as a second layer of authentication for the public entry. Full steps below.

#### Fixed domain + Cloudflare Access (Named Tunnel)

Prerequisite: a domain already onboarded to Cloudflare (DNS hosted by CF; the free plan is fine).

**1. Create a tunnel and get the token** (either way):

- Dashboard: Cloudflare Zero Trust console → Networks → Tunnels → Create tunnel → choose cloudflared → name it (e.g. `adbreeze`) → copy the token (`eyJ...`) from the install page after creation
- CLI: `cloudflared tunnel login` → `cloudflared tunnel create adbreeze` → `cloudflared tunnel token adbreeze`

**2. Bind the public hostname**: Tunnel → Public Hostname → Add: subdomain (e.g. `adb.example.com`) → type **HTTP** → URL `127.0.0.1:9080` (ADBreeze's HTTP port; Cloudflare's edge provides the trusted certificate, nothing to configure locally). CLI alternative: `cloudflared tunnel route dns adbreeze adb.example.com`; ingress rules can live in the CF dashboard or a local `config.yml` — **ADBreeze only needs the token**, and ingress config is authoritative in the CF dashboard.

**3. Paste it into ADBreeze**: Settings → Public Access → paste the token into the tunnel token field → save → enable. The UI showing "Named Tunnel (fixed domain)" means it's live; phones and computers can reach the login page at `https://adb.example.com`.

**4. Add Access (recommended)**: Zero Trust → Access → Applications → Add an application → Self-hosted: any app name, subdomain `adb.example.com`; Policy "Allow" with one entry — your email (one-time PIN) or Google login. From then on, anyone visiting the domain passes Cloudflare's authentication first and only then reaches the ADBreeze login page — **even a leaked ADBreeze password hits a second door**. Access's free tier is plenty for personal use; policies can be changed in the dashboard anytime.

Note: Access only protects Named Tunnels (requires a domain zone); Quick Tunnel's random URL can't use it — it relies on "unguessable if unshared" plus a strong password.

Security constraints (advisory, not blocking — consistent with the LAN policy of "warn-only for the default password"):

- **The default password `admin` can still enable the tunnel**, but the server logs a WARN and the UI shows a reminder: a public URL plus a guessable password is risky; use `-password` / `ADBREEZE_PASSWORD` to set a strong one (restart to apply)
- The tunnel targets `http://127.0.0.1:<HTTP port>` by default: decoupled from whether the service listens externally — tightening `-addr` to localhost-only doesn't affect public access
- The token is passed to the subprocess only via environment variables (never in process arguments, never in logs; the API returns only a redacted preview), stored in plaintext in the local `settings.json` (permissions 0600)
- While the tunnel is running, a **persistent "Public access on" badge** sits in the top bar, visible on every page
- **Auto-restart = remember last state**: if the tunnel was on and never manually "disabled" (including direct exit / crash) → the next ADBreeze start restores it automatically; if it was never enabled, or manually disabled once → no auto-restart until manually enabled again. To turn it off for good: enable once, disable once
- Mirroring over the public internet: signaling through the tunnel is fine; WebRTC media is P2P and bypasses the tunnel — public STUN is preconfigured (`stun.cloudflare.com:3478` + `stun.l.google.com:19302`, overridable via `-webrtc-stun` or disabled with `off`); when both ends can hole-punch it goes P2P automatically; with symmetric NAT where punching fails, it degrades to the WS video channel (works, higher latency); a self-hosted TURN is the real fix

For headless scenarios (Docker / Termux, no browser UI), use launch flags: `-tunnel` to enable, `-tunnel-token <token>` for Named mode; the rest of the config (cloudflared path, protocol, auto-restart) still comes from the Settings page archive, which takes precedence, with the CLI token as fallback when absent. `-tunnel` also remembers its state (restored next launch even without the flag).

### Install as an app (PWA)

The frontend ships a Web App Manifest and Service Worker, so it can be installed as a standalone app:

- **Desktop Chrome / Edge**: an "Install" icon appears at the right of the address bar, or use the menu "App → Install this site as an app"
- **Android Chrome**: menu "Add to Home screen"; afterwards it launches in a standalone window with no browser UI
- **iOS Safari**: Share → "Add to Home Screen". `apple-mobile-web-app-capable` is set, so opening from the home screen icon is fullscreen with the status bar blending in via `black-translucent`

The Service Worker only caches the static shell (HTML/CSS/JS/icons) and **never caches `/api` or `/ws`** — those carry live device data and realtime streams; caching them would show stale state or connect to dead sessions. So offline you can only open the UI; features still require the service online.

Installability is bound to the secure context: installable over `http://localhost` or HTTPS; visiting a LAN IP over `http://` is not considered installable by the browser (needs HTTPS with a trusted CA). Note that browsers judge a "trustworthy origin" by the **hostname**, not the IP it resolves to: `localhost` is inherently trustworthy; a hostname (e.g. `http://my-pc:9080`) requires HTTPS with the certificate SAN covering that hostname — which is why hostnames go into the certificate above.

### Capabilities in non-secure contexts (HTTP + LAN IP)

When visiting via `http://192.168.x.x:9080`, there is no secure context; the actual behavior of each web capability:

| Capability | Behavior |
|---|---|
| Mirroring | Works. Automatic fallback: WebCodecs → WebRTC → WASM software decode (software mode automatically switches the device to its software encoder, outputting Baseline profile) |
| Mirroring audio | Works. Falls back to libopus WASM decoding, reusing the same AudioWorklet playback pipeline |
| Reverse touch / terminal / files / apps / screenshot | Work (plain HTTP is enough) |
| PWA install | Unavailable (needs HTTPS + trusted CA) |

So **when switching environments you don't have to depend on certificates**: use `http://localhost:9080` locally and `http://<IP>:9080` from other devices for full functionality; go through HTTPS when you need PWA install or WebCodecs hardware decoding.

### One-click scripts

> ⚠️ **AI Agent notice**: there are two launch scripts for different users —
> - **`go run ./scripts/agent`**: for AI Agents / automation tools (ports 9081/9444, no clash with the user's instance);
> - **`go run ./scripts`**: for manual use by humans only; **AI Agents must not call it**.
>
> They differ only in default ports; all parameters are identical.

Cross-platform development scripts written in Go (works on Windows / macOS / Linux) chain the whole flow of "frontend build → Go compile → launch":

```bash
# For manual use (default ports 9080/9443)
go run ./scripts                              # default password admin, http://localhost:9080 + https://localhost:9443
go run ./scripts -password yyy                # custom admin password
go run ./scripts -skip-web                    # skip pnpm build when the frontend hasn't changed
go run ./scripts -https=false                 # HTTP only (HTTP is always available)
go run ./scripts -addr :8000 -https-addr :9444  # custom ports

# AI Agent only (default ports 9081/9444, can run alongside the user's instance)
go run ./scripts/agent
```

### Development & debugging (frontend hot reload)

When changing frontend code you don't need to repeat `pnpm run build` + `go build`. The Vite dev server has a proxy configured (`/api` and `/ws` forwarded to the backend on `:9080`); run the two terminals separately:

```bash
# Terminal 1: start the backend (or just go run ./scripts -skip-web)
./bin/adbreeze -password admin                 # Windows: .\bin\adbreeze.exe

# Terminal 2: start the frontend dev server
cd web
pnpm run dev
```

Open `http://localhost:5173` in the browser and enter the admin password `admin` in the login box. From then on, saving any code under `web/src` takes effect immediately (HMR hot reload; mirroring sessions stay alive); only Go code changes require restarting the backend in terminal 1. Note that `pnpm run dev` and the one-click script's frontend build must not run at the same time (both write to `web/dist`).

Connecting devices:

```powershell
adb connect 192.168.1.20:5555   # network device (can also connect from the web page)
# USB devices appear in the list as soon as they're plugged in
```

### Docker

Prebuilt images are published to GHCR (`linux/amd64` + `linux/arm64`, auto-matched to your architecture on pull); just run:

```bash
docker pull ghcr.io/smanx/adbreeze:latest
docker run -d --name adbreeze \
  -p 9080:9080 -p 9443:9443 \
  -v adbreeze-config:/config \
  ghcr.io/smanx/adbreeze:latest
```

The container's default password is `admin`; open `http://localhost:9080` (or `https://localhost:9443`) in the browser and log in. The image ships with adb, so you can `adb connect` network devices (run it inside the container, or connect from the web page). For USB devices, add device passthrough parameters:

```bash
docker run -d --name adbreeze -p 9080:9080 -p 9443:9443 \
  -v adbreeze-config:/config \
  --privileged -v /dev/bus/usb:/dev/bus/usb \
  ghcr.io/smanx/adbreeze:latest
```

**`-v adbreeze-config:/config` is required**: the in-container data directory `/config` stores the auto-generated TLS CA — without the mount, recreating the container regenerates the CA and voids the trust you imported in your browser. With the mount, the CA survives across containers and trust is a one-time step. Password and other configuration can be passed via environment variables (see the configuration table above) without changing the command line:

```bash
docker run -d --name adbreeze -p 9080:9080 -p 9443:9443 \
  -v adbreeze-config:/config \
  -e ADBREEZE_PASSWORD=my-secret \
  ghcr.io/smanx/adbreeze:latest
```

Version tags match Releases (e.g. `ghcr.io/smanx/adbreeze:v0.2.0`); `:latest` always points to the newest release.

You can also build from source:

```bash
docker build -t adbreeze .
docker run -d --name adbreeze -p 9080:9080 -p 9443:9443 -v adbreeze-config:/config adbreeze
```

### Cross-compilation (six platforms, single file)

```bash
make build-all   # windows/amd64 arm64 · darwin/amd64 arm64 · linux/amd64 arm64
```

### Android (Termux)

```bash
pkg install android-tools
./adbreeze-linux-arm64-*        # run the service (artifact names carry the version)
adb connect 127.0.0.1:5555      # wireless-debugging self-connect
```

### Android (APK)

Same shape as the desktop version: **the app is just a service console** — fill in the port / admin password, tap "Start", and logs show in place; no UI is built in — open the service address in a browser (the app has a one-tap "Open page" button) to operate devices.

- **Get it**: download `adbreeze-android-<version>.apk` directly from GitHub Releases (CI packs it into the release alongside desktop platforms after tagging; all artifact filenames carry the version); or build locally with `make android-apk`
- Open the app → fill in the config (default 9080 / admin) → tap "Start" → visit `http://127.0.0.1:<port>` in a browser (LAN devices can visit `http://<phone-IP>:<port>`; the log lists them)
- **Defaults match the PC version**: HTTP + HTTPS dual ports (HTTPS :9443), with a green status-dot marker after startup; for LAN devices to hardware-decode mirroring, download and trust the CA from the web Settings page (without it, use HTTP access and mirroring falls back to WebRTC/software decode)
- Connection is **wireless**: enable "Developer options → Wireless debugging" on the target device, then `adb connect <ip>:<port>` from the web page; or `adb connect 127.0.0.1:5555` to mirror the phone itself
- A persistent notification shows the service address and offers "Stop service"; crashed processes/ports are relaunched automatically
- Architecture and implementation details in [ANDROID.md](ANDROID.md); CI in `.github/workflows/release.yml` (unified release packaging) and `.github/workflows/android.yml` (push build checks)

## API Overview

HTTP endpoints authenticate via sessions: first `POST /api/session` (body `{"password": "..."}`) exchanges the admin password for a session; afterwards, access uses the `HttpOnly` session cookie (carried automatically same-origin) or `Authorization: Bearer <token>` (cross-origin); WebSocket handshakes use the cookie or the `?session=<token>` query parameter. All endpoints except `/api/health` and `/api/session` are protected. Sessions last 12 hours; `DELETE /api/session` logs out.

| Method | Path | Description |
|---|---|---|
| GET | `/api/devices` | Device list |
| POST / DELETE | `/api/devices/connect`, `/api/devices/{serial}` | Connect / disconnect network devices |
| POST | `/api/devices/{serial}/shell` | Run a one-off command |
| POST / GET / DELETE | `/api/devices/{serial}/mirror` | Start / info / stop mirroring |
| POST | `/api/devices/{serial}/webrtc` | Set up a WebRTC video channel (fallback / public-internet P2P) |
| GET | `/api/devices/{serial}/overview` | Device system overview (model / battery / storage, etc.) |
| GET | `/api/devices/{serial}/perf` | Performance sampling (CPU / memory) |
| GET | `/api/devices/{serial}/ui-dump` | UI hierarchy dump (widget tree) |
| GET / POST | `/api/devices/{serial}/apps`, `/apps/info`, `/apps/action`, `/apps/apk` | App list / details / start-stop-uninstall / export APK |
| GET / POST | `/api/devices/{serial}/processes`, `/processes/kill` | Process list / kill process |
| WS | `/ws/video/{serial}`, `/ws/audio/{serial}` | Video stream (binary frames down, JSON control events up) / audio stream |
| WS | `/ws/term/{serial}`, `/ws/logcat/{serial}` | Interactive terminal / live logs |
| WS | `/ws/events` | Device connect/disconnect push |
| GET/PUT/DELETE | `/api/devices/{serial}/files` | Browse / upload / delete files |
| GET | `/api/devices/{serial}/files/content` | File download |
| POST | `/api/devices/{serial}/files/mkdir`, `/files/rename` | Create directory / rename |
| GET/POST | `/api/devices/{serial}/clipboard` | Clipboard read / write |
| GET | `/api/devices/{serial}/screenshot` | Screenshot PNG |
| POST | `/api/devices/{serial}/install` | APK install |
| GET/PUT | `/api/settings/tunnel` | Public access (Cloudflare Tunnel) config read/write |
| POST / GET | `/api/settings/tunnel/install` | One-click cloudflared install (trigger / check progress) |
| GET | `/api/tunnel` | Tunnel status (connectivity / public URL / logs) |
| POST | `/api/tunnel/start`, `/api/tunnel/stop` | Tunnel enable / disable |

## Security Notes

ADB means **full control** of your devices. Guard the password carefully (default `admin` — on public networks always change it with `-password` / `ADBREEZE_PASSWORD`; the startup log warns when the default password is combined with external listening). The service is meant for intranets by default. **Never expose raw HTTP to the internet** — HTTPS is on by default (the self-signed CA is for intranet use only; for public use, provide a real certificate with `-tls-cert` / `-tls-key`) or put the service behind a reverse proxy. The admin password is submitted only once at login; afterwards, traffic carries a 12-hour session token. The session signing key is randomly generated at each startup, so all logged-in sessions must re-authenticate after a restart. The login endpoint has brute-force protection (5 consecutive failures lock out for 30 seconds).

## Directory Structure

```
adbreeze/
├── cmd/adbreeze/          # entrypoint (launch.go double-click experience / console_*.go console code page)
├── internal/
│   ├── adb/               # adb server protocol (smart socket + sync)
│   ├── scrcpy/            # mirroring (server lifecycle / protocol parsing / ControlMessage / SPS)
│   ├── webrtc/            # WebRTC sessions (video fallback for non-secure contexts / public internet)
│   ├── api/               # REST + WebSocket + static frontend
│   ├── auth/              # admin password login + session cookie/token
│   ├── browser/           # launching the system default browser (double-click finishing touch)
│   ├── settings/          # user preference persistence (adb path / tunnel config, written 0600)
│   ├── tlsutil/           # self-signed certificate generation / reuse (HTTPS)
│   ├── tunnel/            # public access: managed cloudflared subprocess (CF-TUNNEL-DESIGN.md)
│   └── config/            # command-line configuration
├── web/                   # frontend source (React + Vite)
│   └── dist/              # frontend build output (embedded into the binary, not distributed with the repo;
│                          #   manifest.webmanifest / sw.js / icons/ produced at build time, powering the PWA)
├── android/               # Android app (Go service embedded as assets, APK form)
├── scripts/               # cross-platform dev scripts: go run ./scripts (manual) / go run ./scripts/agent (AI Agent only)
├── vendor-bin/            # embedded scrcpy-server jar
├── Dockerfile / Makefile / .github/workflows/release.yml
```

## Acknowledgements

- [scrcpy](https://github.com/Genymobile/scrcpy) (Apache-2.0) — the mirroring core `scrcpy-server` and protocol reference
- [ws-scrcpy](https://github.com/DeviceFarmer/ws-scrcpy) — reference material for the protocol implementation

> Note: this project's code is released under the MIT license; the embedded `scrcpy-server` remains under its original Apache-2.0 license.

## License

[MIT](LICENSE)
