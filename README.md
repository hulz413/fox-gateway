# Fox Gateway

Fox Gateway connects Feishu (Lark) chat with a local coding agent runtime.

Right now the project supports:
- Feishu long-connection message delivery
- a local Claude Code worker
- local pairing for the first approver
- a simple `fox-gateway setup` flow for writing local config

At the moment, the only coding agent backend supported is **Claude Code**.

## Quick start

### 1. Run setup

```bash
./fox-gateway setup
```

This writes local runtime config to:

```text
~/.fox-gateway/fox-gateway.json
```

During setup, you will be asked for:
- `LARK_APP_ID`
- `LARK_APP_SECRET`

### 2. Start the gateway

```bash
./fox-gateway
```

### 3. Pair the first approver

When the gateway starts for the first time, it will generate a pair code.
Send the printed pairing message to the Feishu bot from the user account you want to use as the first approver.

### 4. Talk to the bot in Feishu

After pairing, send messages to the Feishu bot and Fox Gateway will forward them to the local Claude Code worker.

## Notes

- Local config and pairing state live under `~/.fox-gateway/`
- Runtime logs are written under `~/.fox-gateway/logs/`
- The gateway currently uses Feishu **websocket / long connection** delivery mode
- The worker currently runs **Claude Code** only
