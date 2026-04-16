# Fox Gateway

Fox Gateway connects Feishu (Lark) chat with a local coding agent runtime.

At the moment, the only coding agent backend supported is Claude Code.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/hulz413/fox-gateway/main/scripts/install.sh | bash
```

This installs the latest GitHub Release binary to `~/.local/bin/fox-gateway`.
The installer script itself is fetched from the repository raw URL, while the binary payload is downloaded from GitHub Releases.

Supported platforms:
- Linux amd64 / x86_64
- macOS Intel (x86_64)
- macOS Apple Silicon (arm64)

If `fox-gateway` is not available in your current shell after install, run:

```bash
source ~/.profile
```

Or open a new shell session.

## Quick start

### 1. Run setup

```bash
fox-gateway setup
```

This writes local runtime config to `~/.fox-gateway/fox-gateway.json` by default.

During setup, you will be asked for:
- `LARK_APP_ID`
- `LARK_APP_SECRET`

### 2. Start the gateway

```bash
fox-gateway start
```

### 3. Pair the first approver

After setup, start the gateway once and use the printed pairing message in the Feishu bot chat from the account you want to use as the first approver.

### 4. Talk to the bot in Feishu

After pairing, send messages to the Feishu bot and Fox Gateway will forward them to the local Claude Code worker.

## Notes

- Local config and pairing state live under `~/.fox-gateway/`
- Runtime logs are written under `~/.fox-gateway/logs/`
- The gateway currently uses Feishu websocket connection delivery mode
- The worker currently runs Claude Code only
