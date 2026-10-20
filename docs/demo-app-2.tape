# Records docs/demo-app-2.gif: payments, inspector and command palette.
#   terminal 1: make mock
#   terminal 2: make demo

Output docs/demo-app-2.gif

Require bash

Set Shell "bash"
Set FontFamily "Menlo"
Set FontSize 13
Set Width 1300
Set Height 760
Set Padding 18
Set WindowBar Colorful
Set TypingSpeed 70ms
Set Framerate 20
Set Theme { "name": "Paygate", "background": "#080b10", "foreground": "#dce8f5", "selection": "#1e2737", "cursor": "#6fb3ff", "black": "#080b10", "red": "#ef4444", "green": "#22c55e", "yellow": "#f59e0b", "blue": "#3d7eff", "magenta": "#a78bfa", "cyan": "#6fb3ff", "white": "#dce8f5", "brightBlack": "#5c6e88", "brightRed": "#f87171", "brightGreen": "#4ade80", "brightYellow": "#fbbf24", "brightBlue": "#6fb3ff", "brightMagenta": "#c4b5fd", "brightCyan": "#93c5fd", "brightWhite": "#ffffff" }

Env PAYGATE_BASE_URL "http://127.0.0.1:8089"
Env PAYGATE_NO_KEYCHAIN "1"
Env PAYGATE_NO_SPLASH "1"
Env XDG_CONFIG_HOME "/tmp/paygate-demo-app2"
Env LANG "en_US.UTF-8"

Hide
Type@1ms "rm -rf /tmp/paygate-demo-app2 && export PATH=$PWD:$PATH PS1='$ ' && paygate init --name demo-shop --merchant-id demo-shop --api-key demo-key >/dev/null && paygate pay --card mc-ok --amount 12.99 -o json >/dev/null && paygate pay --card visa-decline --amount 3.50 -o json >/dev/null; paygate pay --method paypal --amount 24.90 --poll-interval 200ms -o json >/dev/null && paygate pay --card visa-ok --capture manual --amount 49.90 -o json >/dev/null && clear"
Enter
Sleep 4s
Show

Type "paygate"
Enter
Sleep 1.2s
# Payments: open the authorized one and capture it
Type "l"
Sleep 1.2s
Enter
Sleep 1.4s
Type "c"
Sleep 900ms
Enter
Sleep 500ms
Enter
Sleep 1.6s
# Inspector
Type "i"
Sleep 1.2s
Type "v"
Sleep 1.4s
# Command palette
Ctrl+K
Sleep 600ms
Type "prof"
Sleep 1.2s
Escape
Sleep 500ms
