# Records docs/demo.gif against the local mock Paygate (no real credentials).
# Recordings longer than ~20 s crash vhs, so the hosted flow has its own tape.
#   terminal 1: make mock
#   terminal 2: make demo

Output docs/demo.gif

Require bash

Set Shell "bash"
Set FontFamily "Menlo"
Set FontSize 14
Set Width 1100
Set Height 720
Set Padding 22
Set WindowBar Colorful
Set TypingSpeed 40ms
Set Framerate 20
Set Theme { "name": "Paygate", "background": "#080b10", "foreground": "#dce8f5", "selection": "#1e2737", "cursor": "#6fb3ff", "black": "#080b10", "red": "#ef4444", "green": "#22c55e", "yellow": "#f59e0b", "blue": "#3d7eff", "magenta": "#a78bfa", "cyan": "#6fb3ff", "white": "#dce8f5", "brightBlack": "#5c6e88", "brightRed": "#f87171", "brightGreen": "#4ade80", "brightYellow": "#fbbf24", "brightBlue": "#6fb3ff", "brightMagenta": "#c4b5fd", "brightCyan": "#93c5fd", "brightWhite": "#ffffff" }

Env PAYGATE_BASE_URL "http://127.0.0.1:8089"
Env PAYGATE_NO_KEYCHAIN "1"
Env XDG_CONFIG_HOME "/tmp/paygate-demo"

Hide
Type "rm -rf /tmp/paygate-demo && export PATH=$PWD:$PATH PS1='$ ' && paygate init --name demo-shop --merchant-id demo-shop --api-key demo-key >/dev/null && clear"
Enter
Sleep 1s
Show

# 1 · interactive card payment
Type "paygate pay"
Sleep 400ms
Enter
Sleep 1.5s
Enter
Sleep 1.2s
Down
Sleep 300ms
Down
Sleep 300ms
Up
Sleep 300ms
Up
Sleep 500ms
Enter
Sleep 900ms
Backspace 5
Type "49.90"
Sleep 400ms
Enter
Sleep 500ms
Enter
Sleep 500ms
Enter
Sleep 3s

# 2 · a declined card
Type "clear && paygate pay --card visa-decline --amount 3.50"
Sleep 300ms
Enter
Sleep 3.5s

