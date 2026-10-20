# Paygate CLI

Send real Nexi / Computop Paygate payments from your terminal.

![Version](https://img.shields.io/badge/version-0.2.0-3d7eff)
![Go](https://img.shields.io/badge/go-1.27-00add8)
![Status](https://img.shields.io/badge/status-MVP-f59e0b)

> [!WARNING]
> **Unofficial tool.** paygate is a private side project and is not made,
> endorsed or supported by Nexi or Computop. It sends real requests to the
> Paygate API; with a production profile, real money moves. You use it on your
> own responsibility and without any warranty.

![The paygate app: start screen, new payment, receipt](docs/demo-app.gif)

`paygate` talks to the Paygate REST API v2. Type `paygate` for a full-screen
app with a menu, or use single commands in scripts. Create a payment in
seconds, watch a hosted checkout complete live, then capture, refund or
release it. Test cards and a simulation mode for production are built in, and
every request can be inspected or exported as `curl`.

## Features

- **App and commands**: a full-screen app (`paygate`) with menu, key hints and
  command palette, and the same functions as single commands for scripts and CI.
- **All integration types**: hosted payment page, hosted single method (PayPal,
  iDEAL, SEPA, Klarna and more) and direct card payments.
- **Safe on production**: simulation with `Test:0000` / `Test:0305`, 0.01 as
  default amount and manual capture with one-key release.
- **Live status**: checkout URL with QR code; paygate waits until the payment is
  final.
- **Payments and follow-ups**: local history, lookup by ID, capture, refund and
  release with confirmation and automatic amounts.
- **Developer friendly**: Inspector with every request and response, `-o json`,
  `--curl`, HTTP logging with redacted secrets, test card aliases and multiple
  merchant profiles. English and German interface.

![Payments, capture, Inspector and command palette](docs/demo-app-2.gif)

<sub>All recordings run against the built-in mock server (`make mock`, `make demo`).</sub>

## Installation

You need about five minutes. Every step shows what you should see, so you know
it worked before you continue. The repository is private for now: you need
access to it, and Git asks you to sign in to GitHub when you download it.

<details open>
<summary><b>macOS</b></summary>

**1. Open the Terminal.** Press <kbd>⌘ Cmd</kbd> + <kbd>Space</kbd>, type
`Terminal` and press <kbd>Enter</kbd>.

**2. Install Homebrew** (skip this step if `brew --version` already prints a
version number):

```bash
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
```

At the end, Homebrew prints "Next steps" with two or three commands. Copy and
run them, then close the Terminal and open a new one.

**3. Install Go and Git:**

```bash
brew install go git
```

Check: `go version` prints something like `go version go1.27.3 darwin/arm64`.

**4. Download the Paygate CLI:**

```bash
git clone https://github.com/krebs3r/paygate-cli.git ~/paygate-cli
```

**5. Run the installer:**

```bash
~/paygate-cli/scripts/install.sh
```

It builds `paygate`, copies it to `~/.local/bin` and adds that folder to your
`PATH`. It ends with `Done. Next steps`.

**6. Close the Terminal window and open a new one.** Only new windows know the
updated `PATH`.

**7. Check the installation:**

```bash
paygate --version
```

You should see `paygate 0.2.0`. Continue with the [Quickstart](#quickstart).

</details>

<details open>
<summary><b>Windows 10 / 11</b></summary>

**1. Open PowerShell.** Click Start, type `PowerShell` and press
<kbd>Enter</kbd>. (Windows Terminal works too and shows colours and QR codes
best.)

**2. Install Go and Git:**

```powershell
winget install --id GoLang.Go -e
winget install --id Git.Git -e
```

Confirm the prompts, then **close PowerShell and open a new window**.
Check: `go version` prints something like `go version go1.27.3 windows/amd64`.

**3. Download the Paygate CLI:**

```powershell
git clone https://github.com/krebs3r/paygate-cli.git $HOME\paygate-cli
```

**4. Run the installer:**

```powershell
powershell -ExecutionPolicy Bypass -File $HOME\paygate-cli\scripts\install.ps1
```

It builds `paygate.exe`, copies it to `%LOCALAPPDATA%\Programs\paygate` and adds
that folder to your `PATH`. It ends with `Done. Next steps`.

**5. Close PowerShell and open a new window.**

**6. Check the installation:**

```powershell
paygate --version
```

You should see `paygate 0.2.0`. Continue with the [Quickstart](#quickstart).

</details>

### Update

Download the latest version and run the installer again (macOS shown; on
Windows use the commands from step 3 and 4 above):

```bash
git -C ~/paygate-cli pull
~/paygate-cli/scripts/install.sh
```

### Uninstall

| | Command |
| --- | --- |
| macOS | `~/paygate-cli/scripts/install.sh --uninstall` |
| Windows | `powershell -ExecutionPolicy Bypass -File $HOME\paygate-cli\scripts\install.ps1 -Uninstall` |

Your profiles and API keys are kept. Remove them first with
`paygate profile delete <name>` if you want a clean slate.

### Still `command not found`?

| Symptom | Fix |
| --- | --- |
| macOS: `zsh: command not found: paygate` | You are still in the old window. Close it and open a new one. If it persists, run `source ~/.zshrc`. |
| Windows: `paygate : The term 'paygate' is not recognized` | Close **all** PowerShell windows and open a new one. If it persists, sign out of Windows and back in. |
| `command not found: go` | Go is missing or the window is too old. Repeat step 3 (macOS) or 2 (Windows) and open a new window. |
| Windows: "running scripts is disabled on this system" | Start the installer exactly as shown, with `-ExecutionPolicy Bypass`. |

## Quickstart

Type `paygate` and press <kbd>Enter</kbd>. The full-screen app opens: pick an
area in the menu on the left with the arrow keys or the mouse, the bottom line
always shows the keys you can use, <kbd>?</kbd> lists all shortcuts and
<kbd>ctrl</kbd>+<kbd>k</kbd> opens a command palette that finds every action
by typing a few letters (English or German). On first start it walks you
through adding your merchant.

| Area | What you do there |
| --- | --- |
| Home | Active profile, credential check, today's numbers, latest payments |
| New payment | Form, review (request JSON, copy as curl), send, receipt; QR code and live status for hosted pages |
| Payments | Local history with filter (`/`), lookup by ID (`g`), details with capture, refund, release and refresh |
| Test cards | Test cards with aliases; `y` copies a number |
| Inspector | The last 200 requests to Paygate from the app and the command line: request and response with headers and JSON, timing, correlation ID; copy body or curl |
| Profiles | Add (with credential check), switch, verify, delete |
| Settings | Language, start screen, currency, payment page language, callback URLs, customer IP |

Everything in the app also works as a single command, for scripts and CI.

Prefer single commands, for example in scripts?

```bash
paygate init                       # 1. add merchant ID + REST API key
paygate pay                        # 2. send a payment with the wizard
paygate get <payId>                # 3. look at it again later
```

1. **`paygate init`** asks for your merchant ID and REST API key (Paygate Merchant
   Portal → Key management), checks them and stores the key in your system
   keychain.
2. **`paygate pay`** guides you through flow, test card, amount and currency.
3. **`paygate get`** shows status, amounts and response codes of any payment.

## Commands

Single commands work without the app, for example in scripts:

![paygate pay: card payment and a declined card](docs/demo.gif)

![paygate pay --hosted: QR code and live status](docs/demo-hosted.gif)

| Command | Purpose | Example |
| --- | --- | --- |
| `paygate` | Open the full-screen app | `paygate` |
| `paygate init` | Add or update a merchant profile | `paygate init` |
| `paygate pay` | Create a payment (wizard without flags) | `paygate pay --card visa-ok --amount 12.99` |
| `paygate pay --hosted` | Hosted payment page with all or selected methods | `paygate pay --hosted --methods card,paypal --open` |
| `paygate pay --method` | Hosted form for a single method | `paygate pay --method ideal --amount 5` |
| `paygate get` | Show a payment by payId or transId | `paygate get --trans-id TIDMVGGX4YZ9I9APE` |
| `paygate history` | List payments created or looked up with paygate | `paygate history --limit 50` |
| `paygate capture` | Capture an authorized payment | `paygate capture <payId>` |
| `paygate refund` | Refund fully or partially | `paygate refund <payId> --amount 5` |
| `paygate cancel` | Cancel (reverse) before capture | `paygate cancel <payId>` |
| `paygate testcards` | List test cards and aliases | `paygate testcards` |
| `paygate profile` | List, switch, show, edit, delete profiles | `paygate profile use shop-prod` |
| `paygate about` | Version, license and credits | `paygate about` |

Global flags work with every command:

| Flag | Effect |
| --- | --- |
| `-p, --profile <name>` | Use another profile for this call |
| `-o json` | Print the raw API response as JSON (pipe it into `jq`) |
| `--curl` | Print the request as a `curl` command instead of sending it |
| `-v` / `-vv` | Log HTTP bodies / headers to stderr, credentials redacted |
| `-y, --yes` | Skip confirmations (required in scripts for capture, refund, cancel) |
| `--no-color` | Plain output (`NO_COLOR` works too) |

## Configuration and security

- **Profiles** live in `~/.config/paygate-cli/config.toml` on macOS and Linux and
  in `%APPDATA%\paygate-cli\config.toml` on Windows (readable only by you).
  Each profile stores platform (Nexi or Computop), environment, merchant ID and
  auth mode (Basic Auth or OAuth2).
- **API keys** are kept in the macOS Keychain, Windows Credential Manager or the
  Linux Secret Service. Without a keychain, paygate falls back to the config file
  and says so.
- **Test first**: new profiles default to the test system. Production profiles
  show a red `PROD` badge and ask before sending anything.
- **Callback URLs**: Paygate only calls HTTPS URLs on port 443, so `localhost`
  cannot receive redirects or webhooks. paygate does not need them: it polls the
  payment status itself. By default the customer lands on
  `https://paygate.paytest.dev/cli/return`. Use your own URLs per profile:

  ```bash
  paygate profile set return-url  https://shop.example.com/paygate/return
  paygate profile set webhook-url https://shop.example.com/paygate/webhook
  ```

| Environment variable | Purpose |
| --- | --- |
| `PAYGATE_PROFILE` | Profile to use |
| `PAYGATE_API_KEY` | API key override (CI) |
| `PAYGATE_BASE_URL` | Send requests to another host (mock server, proxy) |
| `PAYGATE_NO_KEYCHAIN` | Store keys in the config file instead of the keychain |
| `PAYGATE_NO_HISTORY` | Do not keep the local payment history and request log |
| `PAYGATE_NO_SPLASH` | Skip the start screen of the app |

## Test transactions on production

Paygate treats a payment whose order description is `Test:0000` as a test
transaction, even on production; `Test:0305` simulates a decline. paygate calls
this **simulation**:

```bash
paygate pay --card visa-ok --simulate ok        # Test:0000, succeeds
paygate pay --card visa-ok --simulate decline   # Test:0305, declined
```

In the app, New payment switches the simulation on by default for production
profiles, suggests 0.01 as amount and uses manual capture, so a real
authorization can be released with one key right after.

## Test data

`paygate testcards` lists the official test cards with short aliases such as
`visa-ok`, `visa-decline` or `visa-3ds-challenge`. Any future expiry date and any
CVC work; the 3-D Secure challenge OTP is `1234`. Details are in the
[Nexi test card guide](https://docs.paygate.nexigroup.com/nexi-developer/test-credit-card).

**No credentials yet?** Start the built-in mock server and try every flow
locally:

```bash
make mock                                          # terminal 1
export PAYGATE_BASE_URL=http://127.0.0.1:8089      # terminal 2
paygate init --name mock --merchant-id demo-shop --api-key demo-key
paygate pay
```

## Troubleshooting

| Problem | Solution |
| --- | --- |
| `401 Unauthorized` | Check merchant ID, API key and auth mode with `paygate profile show`, then run `paygate init` again. |
| Every lookup returns `404` | The payment was created on another system. Check test vs. production and Nexi vs. Computop in the profile. |
| Test payments are rejected | Paygate Support has to enable the test system (and 3-D Secure) for your merchant ID. |
| "3-D Secure authentication is required" | Use a non-3DS card such as `visa-ok`, add `--skip-3ds` if your MID allows it, or pay with `--hosted`. |
| Waiting timed out | The customer did not finish the checkout. Check later with `paygate get --trans-id <id>`. |

## License

Copyright © 2026 Martin Krebs. All rights reserved. A public license will be
announced with the first public release.

## Credits

Paygate CLI is made by **Martin Krebs**, author of the
[Paygate Payment Tester](https://paygate.paytest.dev).

The command design was inspired by [mollie-cli](https://github.com/fjbender/mollie-cli)
by **Florian Bender** and **Hannes Reinberger**. Thank you for showing how
pleasant a payments CLI can be.

Built with [Cobra](https://github.com/spf13/cobra) and the
[Charm](https://charm.sh) libraries Bubble Tea, Bubbles, Lip Gloss and Huh.

This is an independent developer tool and not an official product of Nexi or
Computop. Product names are trademarks of their respective owners.
