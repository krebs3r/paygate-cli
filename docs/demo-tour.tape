# Records docs/demo-tour.gif: the hosted payment page tour in demo mode,
# its sequence diagram step by step. Needs no mock: the demo runs inside
# paygate, and a dummy `open` keeps the browser closed.
#   make demo
# vhs crashes on recordings longer than ~20 s, so each tape stays short.

Output docs/demo-tour.gif

Require bash

Set Shell "bash"
Set FontFamily "Menlo"
Set FontSize 16
Set Width 1300
Set Height 760
Set Padding 18
Set WindowBar Colorful
Set TypingSpeed 60ms
Set Framerate 20
Set Theme { "name": "Paygate", "background": "#080b10", "foreground": "#dce8f5", "selection": "#1e2737", "cursor": "#6fb3ff", "black": "#080b10", "red": "#ef4444", "green": "#22c55e", "yellow": "#f59e0b", "blue": "#3d7eff", "magenta": "#a78bfa", "cyan": "#6fb3ff", "white": "#dce8f5", "brightBlack": "#5c6e88", "brightRed": "#f87171", "brightGreen": "#4ade80", "brightYellow": "#fbbf24", "brightBlue": "#6fb3ff", "brightMagenta": "#c4b5fd", "brightCyan": "#93c5fd", "brightWhite": "#ffffff" }

Env PAYGATE_NO_SPLASH "1"
Env XDG_CONFIG_HOME "/tmp/paygate-demo-tour"
Env LANG "en_US.UTF-8"

Hide
Type@1ms "rm -rf /tmp/paygate-demo-tour && mkdir -p /tmp/paygate-demo-tour/bin && printf '#!/bin/sh\nexit 0\n' > /tmp/paygate-demo-tour/bin/open && chmod +x /tmp/paygate-demo-tour/bin/open && export PATH=/tmp/paygate-demo-tour/bin:$PWD:$PATH PS1='$ ' && paygate demo scenario checkout pay >/dev/null && clear"
Enter
Sleep 2s
Show

Type "paygate demo"
Enter
Sleep 1.4s
# The tours of the demo
Type "t"
Sleep 1.6s
# Hosted Payment Page: the diagram marks the current step
Enter
Sleep 2s
# 1 · session: the request is sent, the answer shows
Enter
Sleep 1.3s
# 2 · redirect to the payment page, 3 · the customer pays (waits)
Enter
Sleep 900ms
Enter
Sleep 3.2s
# 4 · return, 5 · webhook
Enter
Sleep 1.3s
Enter
Sleep 1.4s
# 6 · status, 7 · capture
Enter
Sleep 600ms
Enter
Sleep 1.1s
Enter
Sleep 600ms
Enter
Sleep 2s

Hide
Ctrl+C
Sleep 300ms
