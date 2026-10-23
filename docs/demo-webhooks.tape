# Records docs/demo-webhooks.gif: listening for webhooks in the app.
#   make demo (starts its own mock on 127.0.0.1:8090 with Enhanced
#   webhooks and quick hosted payments; no make mock needed for this one)
# vhs crashes on recordings longer than ~20 s, so each tape stays short.

Output docs/demo-webhooks.gif

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

Env PAYGATE_BASE_URL "http://127.0.0.1:8090"
Env PAYGATE_NO_KEYCHAIN "1"
Env PAYGATE_NO_SPLASH "1"
Env XDG_CONFIG_HOME "/tmp/paygate-demo-webhooks"
Env LANG "en_US.UTF-8"

Hide
Type@1ms "go build -o /tmp/paygate-demo-mock ./tools/mockpaygate && (/tmp/paygate-demo-mock -addr 127.0.0.1:8090 -webhook enhanced -not-found-polls 0 -pending-polls 1 >/dev/null 2>&1 &) && rm -rf /tmp/paygate-demo-webhooks && export PATH=$PWD:$PATH PS1='$ ' && sleep 1 && paygate init --name demo-shop --merchant-id demo-shop --api-key demo-key >/dev/null && printf demo-hmac-key | paygate profile set hmac-key >/dev/null && clear"
Enter
Sleep 5s
Show

Type "paygate"
Enter
Sleep 1.2s
# Webhooks: listen (the mock runs on this computer, so no tunnel)
Type "w"
Sleep 1.1s
Space
Sleep 1.6s
# A hosted payment: its webhook arrives while the app waits
Type "n"
Sleep 700ms
Down
Sleep 300ms
Enter
Sleep 300ms
Enter
Sleep 300ms
Enter
Enter
Enter
Enter
Enter
Sleep 1.1s
Enter
Sleep 4s
# The received webhook, its checks, and its payment
Type "w"
Sleep 1.4s
Enter
Sleep 2.2s
Type "g"
Sleep 2.2s

Hide
Ctrl+C
Sleep 500ms
Type@1ms "pkill -f paygate-demo-mock"
Enter
Sleep 300ms
