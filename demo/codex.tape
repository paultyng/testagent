# vhs script for the codex-emulation README demo. Render with: vhs demo/codex.tape

Output demo/codex.gif

Set FontSize 16
Set Width 1100
Set Height 600
Set Theme "Catppuccin Mocha"
Set Padding 24
Set TypingSpeed 30ms

# Force lipgloss + glamour to render with full color even though
# vhs runs in a headless terminal that auto-detects no-tty.
Env CLICOLOR_FORCE "1"
Env COLORTERM "truecolor"
Env TERM "xterm-256color"

# Launch via `codex resume` so the banner shows a stable session id and
# "resumed" label — a codex-shaped subcommand the claude tape doesn't exercise.
# Codex has no --name / -n flag (banner label is fixed "session"); the
# claude tape uses -n 'Demo' for its banner. Session id is UUID-v4 to match
# real codex (see cmd/codex/session.go and COMPATIBILITY.md).
Type@15ms "testagent --think-delay 5s codex resume 7c2f1d18-9d4a-4d9d-9b6a-1c0a8b3e5e22 -m gpt-5 -s workspace-write"
Enter
Sleep 1s

# Submit a prompt → triggers the thinking spinner.
Type "what's testagent?"
Enter
Sleep 1500ms

# Type a slash command WHILE thinking is in flight — it queues.
Type "/panel queued mid-think"
Enter
Sleep 4500ms

# Tool-use cycle (runs after the queue drains).
Type@40ms `/fake-tool apply_patch {"path":"main.go"}`
Enter
Sleep 400ms

Type@40ms `/fake-tool-result {"ok":true,"applied":1}`
Enter
Sleep 800ms

Type "/panel codex argv, ~/.codex/config.toml, shell-runner hooks — all deterministic"
Enter
Sleep 1500ms

# /clear demonstrates the lifecycle reset (SessionEnd → SessionStart with reason=clear).
Type "/clear"
Enter
Sleep 1500ms

# Quit codex-style (codex aliases /exit to /quit; both work).
Type "/quit"
Sleep 1500ms
Enter
Sleep 1500ms
