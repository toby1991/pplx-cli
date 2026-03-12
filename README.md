# pplx

> Drive your Perplexity Pro subscription as an MCP tool — the Desktop App does the searching, you pay nothing extra.

[中文文档](README.zh.md)

`pplx` is a macOS CLI and MCP server that controls the **Perplexity Desktop App** via Accessibility automation, turning your existing Pro subscription into a programmable search backend for Claude, OpenCode, and any other MCP client. A direct Sonar REST API backend is also included as a fallback.

---

## Quick Start

```bash
# 1. Build
git clone https://github.com/toby1991/pplx-cli
cd pplx-cli
make install          # builds → /usr/local/bin/pplx

# 2. Grant Accessibility permission (one-time)
#    System Settings → Privacy & Security → Accessibility → add your terminal app

# 3. Verify it works
pplx "what is MCP?"

# 4. Add to Claude Desktop or OpenCode (see MCP section below)
```

**Headless Mac (Mac mini via SSH)?** One extra step:

```bash
pplx setup-caffeinate   # prevents sleep + disables screen lock — run once, persists across reboots
```

---

## How It Works

Two backends, one binary:

| Backend | How | Cost | Speed |
|---------|-----|------|-------|
| **UI** (default) | Controls Perplexity Desktop App via macOS AXUIElement API | Free — uses your Pro subscription | 10–30 s |
| **API** | Calls Perplexity Sonar REST API directly | $5/month free tier + API key | 2–5 s |

Two operation modes:

| Mode | Command | Use case |
|------|---------|----------|
| **CLI** | `pplx "query"` | Direct terminal use, scripts, pipes |
| **MCP Server** | `pplx mcp` | Tool calls from Claude, OpenCode, etc. |

---

## CLI Usage

```bash
# Basic search
pplx "best practices for Go error handling"

# Specify model (name prefix — matches the UI model switcher)
pplx --model "Claude Sonnet" "explain monads"

# Specify content sources
pplx --sources web,academic "recent papers on RAG"

# Pipe in, pipe out
echo "what is entropy?" | pplx
pplx "top Go CLI libraries" --json | jq '.answer'

# Quiet mode — answer only, no citations
pplx -q "capital of France"

# Interactive REPL
pplx
```

### Subcommands

```bash
pplx status               # current mode/model (reads Perplexity UserDefaults)
pplx models               # list available UI models
pplx sources              # list content source categories
pplx dump                 # dump AX tree (diagnostic)
pplx version

# Headless Mac management
pplx setup-caffeinate     # install caffeinate LaunchAgent + disable screen lock
pplx remove-caffeinate    # uninstall

# API backend
pplx api "query"          # search via REST API
pplx api --model sonar-reasoning "explain P=NP"
pplx api models           # list API models
```

### Output Formats

**TTY** — colored output with spinner:
```
⠋ Searching...

────────────────────────────────────────────────────
Go error handling best practices include wrapping errors...

Sources:
  [1] Effective Go — https://go.dev/doc/effective_go
  [2] Go Blog: Error handling — https://go.dev/blog/error-handling-and-go
────────────────────────────────────────────────────
```

**Pipe** — plain text, no color or spinner.

**JSON** (`--json`):
```json
{
  "answer": "Go error handling best practices...",
  "citations": [
    {"index": 1, "title": "Effective Go", "url": "https://go.dev/doc/effective_go"}
  ],
  "mode": "search",
  "model": "sonar_pro"
}
```

---

## MCP Server

`pplx mcp` exposes Perplexity as MCP tools over stdio.

### Tools

| Tool | Description |
|------|-------------|
| `search` | Search Perplexity — optional `model` and `sources` override |
| `list_models` | List available models for the active backend |
| `list_sources` | List content source categories (UI backend only) |

### Backend Configuration

```bash
pplx mcp                                    # UI only (default)
pplx mcp --primary api                      # API only
pplx mcp --primary ui --fallback api        # UI primary, API fallback
pplx mcp --primary ui --fallback api \
  --primary-model "GPT-5" \
  --fallback-model sonar-pro
pplx mcp --sources web,academic             # default sources for UI backend
```

### OpenClaw

[OpenClaw](https://openclaw.ai) manages MCP servers through [mcporter](https://github.com/steipete/mcporter). Add the following entry to `~/.mcporter/mcporter.json`:

```json
{
  "mcpServers": {
    "pplx": {
      "command": "/usr/local/bin/pplx",
      "args": ["mcp", "--primary", "api", "--primary-model", "sonar",
               "--fallback", "ui", "--fallback-model", "GPT-5",
               "--sources", "web,academic,social"],
      "env": {
        "PERPLEXITY_API_KEY": "pplx-...",
        "PPLX_PROMPT_SUFFIX": "..."
      }
    }
  }
}
```

### Claude Desktop

`~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "perplexity": {
      "type": "stdio",
      "command": "/usr/local/bin/pplx",
      "args": ["mcp", "--primary", "ui", "--fallback", "api"],
      "env": {
        "PERPLEXITY_API_KEY": "pplx-..."
      }
    }
  }
}
```

### OpenCode

`~/.config/opencode/opencode.json`:

```json
{
  "mcp": {
    "perplexity": {
      "type": "stdio",
      "command": "/usr/local/bin/pplx",
      "args": ["mcp", "--primary", "ui", "--fallback", "api", "--primary-model", "GPT-5"],
      "env": {
        "PERPLEXITY_API_KEY": "pplx-..."
      }
    }
  }
}
```

### MCP Behaviors

- **Search prompt suffix**: In MCP mode, a system prompt is appended to each query, instructing Perplexity to search more deeply and return authoritative sources.
- **Auto-launch**: If Perplexity Desktop is not running, it is started automatically.
- **NavigateToHome**: Before each UI search, the app returns to the home page to start a clean thread.
- **Caffeinate check**: At startup, if using the UI backend, warns if `caffeinate` is not running and suggests `pplx setup-caffeinate`.

---

## Headless Mac Setup

Running on a Mac mini without a display (SSH / remote)?

```bash
# One command handles both sleep prevention and screen lock:
pplx setup-caffeinate
```

This does two things:
1. Installs a LaunchAgent that runs `caffeinate -dimsu` at login (auto-restarts, survives reboots)
2. Disables screen lock via `sysadminctl` (requires your login password)

**Why screen lock matters**: when a remote session (VNC/SSH) disconnects, macOS triggers the lock screen. This degrades WindowServer to "application" mode — all AX automation calls return empty/stale data silently. Disabling screen lock prevents this.

Additional belt-and-suspenders steps for headless:

```bash
sudo pmset -a displaysleep 0      # belt-and-suspenders: disable display sleep via pmset
```

### Display Settings

Two System Settings tweaks prevent the display from sleeping or locking while unattended:

| Setting | Path | Value |
|---------|------|-------|
| Turn off display when inactive | System Settings → Displays | **Never** |
| Require password after screensaver / display off | System Settings → Lock Screen | **Never** |

### Virtual Display

Perplexity requires at least one display (physical or virtual). Options:

1. **Apple Remote Management** *(try this first)* — Enabling Remote Management in System Settings → Sharing automatically exposes a virtual framebuffer on Apple Silicon Mac mini. No extra hardware needed.
2. **HDMI Dummy Plug** — Plug a cheap HDMI dummy plug into the Mac mini's HDMI port. macOS sees it as a real display. Most reliable option if Remote Management doesn't work.
3. **BetterDisplay** — [BetterDisplay](https://github.com/waydabber/BetterDisplay) can create a software virtual display without any hardware. Useful when no physical HDMI port is available (e.g., Mac mini with only USB-C).

- **Accessibility permission**: Grant to the process running `pplx` in System Settings → Privacy & Security → Accessibility.

---

## Architecture

```
┌─────────────────────────────────────────────────┐
│                    cmd/                          │
│  root.go  mcp.go  api.go  caffeinate.go  ...    │
│            Cobra CLI + MCP Server                │
└───────────────────┬─────────────────────────────┘
                    │
          ┌─────────┴──────────┐
          │      driver/        │
          │  search.go          │  ← Dispatcher (primary/fallback)
          │  perplexity.go      │  ← UI backend (AX automation)
          │  api.go             │  ← API backend (Sonar REST)
          └─────────┬───────────┘
                    │
          ┌─────────┴───────────┐
          │    automation/       │
          │  ax.go  (CGo)        │
          │  ax.h   (C header)   │
          │  ax.m   (Obj-C impl) │
          │  macOS AXUIElement   │
          └──────────────────────┘
```

### Search Flow — UI Backend

```
NavigateToHome()       → click chevron-left if on results page
SetModel(model)        → open model popover, select by prefix
SetSources(sources)    → open sources popover, toggle checkboxes
Search(query)
  → open perplexity-app://search?q=...   URL scheme triggers search
  → waitForButtonWithScroll("copy")      poll for generation complete
      → HasButton("checkbox")            generating = true while present
      → GetContentLength()               track answer growth
      → ScrollToBottom()                 handle virtualized viewport
  → Click(copyBtn) + ReadClipboard()     extract answer
  → parseClipboardContent()              parse answer + citations
```

### Search Flow — API Backend

```
APISearch(apiKey, model, query)
  → POST https://api.perplexity.ai/chat/completions
  → parse response + citations
```

---

## Project Structure

```
pplx-cli/
├── main.go
├── go.mod / go.sum
├── Makefile
├── README.md / README.zh.md
├── cmd/
│   ├── root.go          # root command, flags, search dispatch
│   ├── mcp.go           # MCP server subcommand
│   ├── api.go           # API search subcommand
│   ├── caffeinate.go    # setup-caffeinate / remove-caffeinate
│   ├── status.go
│   ├── models.go
│   ├── sources.go
│   ├── dump.go          # AX tree dump (diagnostic)
│   └── version.go
├── driver/
│   ├── perplexity.go    # UI backend: Search, SetModel, SetSources, NavigateToHome
│   ├── search.go        # Dispatcher: primary/fallback routing
│   └── api.go           # API backend: Sonar REST client
├── automation/
│   ├── ax.go            # Go/CGo bindings
│   ├── ax.h             # C header
│   └── ax.m             # Objective-C: AXUIElement implementation
├── output/
│   └── format.go        # TTY detection, spinner, colors, JSON
└── docs/plans/          # Design documents
```

---

## Requirements

- macOS (Apple Silicon or Intel)
- Go 1.23+
- Perplexity Desktop App (for UI backend)
- Accessibility permission granted to terminal / calling process (for UI backend)
- `PERPLEXITY_API_KEY` environment variable (for API backend, optional)

---

## Known Limitations

| Limitation | Detail |
|------------|--------|
| macOS only | Uses AXUIElement API — no cross-platform support |
| Serial execution | One UI search at a time (Perplexity App constraint) |
| Clipboard briefly overwritten | UI backend uses clipboard to read results; original content is restored after |
| Display required | UI backend needs at least one display (physical or virtual) |
| Accessibility permission required | Grant in System Settings → Privacy & Security → Accessibility |

---

## License

MIT
