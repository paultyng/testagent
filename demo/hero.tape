# vhs script for the README hero. Render with: vhs demo/hero.tape

Output demo/hero.gif

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

# Type the launch command quickly — uuid is the long part, so blast it.
Type@15ms "testagent -n 'Demo' --delay 5s --session-id 3ebe43d4-9ab2-4842-b3cd-237fce2ba36f"
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
Type@40ms `/tool read_file {"path":"hello.go"}`
Enter
Sleep 400ms

Type@40ms `/result {"ok":true,"size":42}`
Enter
Sleep 800ms

Type "/panel fake CLI agent for orchestrator tests — no LLM, no network, no $$$"
Enter
Sleep 1500ms

# Linger on /exit before pressing Enter so altscreen teardown
# doesn't whip back to a blank canvas immediately.
Type "/exit"
Sleep 1500ms
Enter
Sleep 1500ms
