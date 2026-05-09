# vhs script for the README hero. Render with: vhs demo/hero.tape

Output demo/hero.gif

Set FontSize 16
Set Width 1100
Set Height 540
Set Theme "Catppuccin Mocha"
Set Padding 24
Set TypingSpeed 60ms

# Force lipgloss + glamour to render with full color even though
# vhs runs in a headless terminal that auto-detects no-tty.
Env CLICOLOR_FORCE "1"
Env COLORTERM "truecolor"
Env TERM "xterm-256color"

Type "testagent -n 'Demo' --session-id 3ebe43d4-9ab2-4842-b3cd-237fce2ba36f --delay 5s"
Enter
Sleep 800ms

# A regular prompt → triggers the thinking spinner with elapsed counter.
Type "what's testagent?"
Enter
Sleep 5500ms

# Tool-use cycle
Type@40ms `/tool read_file {"path":"hello.go"}`
Enter
Sleep 400ms

Type@40ms `/result {"ok":true,"size":42}`
Enter
Sleep 700ms

# Bordered panel
Type "/panel fake CLI agent for orchestrator tests — no LLM, no network"
Enter
Sleep 1200ms

Type "/exit"
Enter
Sleep 400ms
