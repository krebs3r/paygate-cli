# Records docs/demo-app.gif: the full-screen app against the local mock.
#   terminal 1: make mock
#   terminal 2: make demo
# vhs crashes on recordings longer than ~20 s, so each tape stays short.

Output docs/demo-app.gif

Require bash

Set Shell "bash"
Set FontFamily "Menlo"
Set FontSize 13
Set Width 1300
Set Height 760
Set Padding 18
Set WindowBar Colorful
Set TypingSpeed 60ms
Set Framerate 20
Set Theme { "name": "Paygate", "background": "#080b10", "foreground": "#dce8f5", "selection": "#1e2737", "cursor": "#6fb3ff", "black": "#080b10", "red": "#ef4444", "green": "#22c55e", "yellow": "#f59e0b", "blue": "#3d7eff", "magenta": "#a78bfa", "cyan": "#6fb3ff", "white": "#dce8f5", "brightBlack": "#5c6e88", "brightRed": "#f87171", "brightGreen": "#4ade80", "brightYellow": "#fbbf24", "brightBlue": "#6fb3ff", "brightMagenta": "#c4b5fd", "brightCyan": "#93c5fd", "brightWhite": "#ffffff" }

Env PAYGATE_BASE_URL "http://127.0.0.1:8089"
Env PAYGATE_NO_KEYCHAIN "1"
Env XDG_CONFIG_HOME "/tmp/paygate-demo-app"
Env LANG "en_US.UTF-8"

Hide
Type@1ms "rm -rf /tmp/paygate-demo-app && export PATH=$PWD:$PATH PS1='$ ' && paygate init --name demo-shop --merchant-id demo-shop --api-key demo-key >/dev/null && clear"
Enter
Sleep 1s
Show

Type "paygate"
Enter
Sleep 3.2s
# New payment: card, visa-ok, amount 49.90
Type "n"
Sleep 900ms
Enter
Sleep 700ms
Enter
Sleep 600ms
Backspace 5
Type "49.90"
Enter
Sleep 300ms
Enter
Sleep 300ms
Enter
Sleep 300ms
Enter
Sleep 300ms
Enter
Sleep 1.4s
Type "j"
Sleep 1.4s
Enter
Sleep 2.6s
Type "d"
Sleep 1.6s
