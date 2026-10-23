# Changelog

All notable changes to this project are documented in this file. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

## [0.6.1]

### Fixed

- The webhook list in the app sizes its columns to their content, so a
  status such as AUTHORIZED, a large amount or a date from another day no
  longer runs into the next column and moves the IDs. The status line goes
  below the title when both do not fit on one line.

## [0.6.0]

### Added

- Webhooks in the app (`w`): space starts and stops listening, through a
  cloudflared quick tunnel or, against the mock server, directly. The list
  of received webhooks updates by itself, also with those a `paygate listen`
  in another terminal receives, and a dot in the menu shows a running
  listener. Enter shows a webhook with all checks, headers and body, `r`
  sends it again to your handler and `g` opens its payment. Quitting the app
  or switching the profile closes the tunnel.
- The payment detail in the app lists the payment's webhooks, in the card
  and in the timeline.
- Command palette: start and stop listening, delete the received webhooks.

### Changed

- In the app, q asks before quitting while the app listens for webhooks or
  waits for a payment; otherwise it quits at once, and ctrl+c always does.
- In the app, esc on the receipt of a successful payment returns to the
  menu and leaves the receipt as it is; shift+tab still opens the review.
  After a failed payment esc goes back to the review, as before.
- `paygate webhooks verify` and `show` wrap long findings below their detail
  column.

## [0.5.0]

### Added

- `paygate listen` receives Paygate's webhooks on this computer, through a
  cloudflared quick tunnel (no account needed) or an address of your own
  (`--public-url`). It checks every webhook, confirms it with a GET of the
  payment, keeps it locally and marks repeated deliveries. `--forward-to`
  passes it on unchanged to your own handler and gives Paygate its status;
  `--respond` sets the status yourself.
- Hosted payments send their webhook to a running `paygate listen` of the
  same profile, from `paygate pay` and from the app. `paygate pay --listen`
  opens a tunnel just for one payment and waits for its webhook.
- `paygate webhooks list`, `show`, `resend` and `clear` for the received
  webhooks. `PAYGATE_CLOUDFLARED` points to cloudflared if it is not on the
  `PATH`.
- `paygate init` and Profiles → Add in the app ask whether to store the
  webhook HMAC key right away. For an existing profile, Settings → Webhook
  HMAC key in the app shows whether one is stored and sets, replaces or
  removes it.

### Changed

- `paygate webhooks send` builds Basic and Enhanced webhooks the way Paygate
  sends them. `verify`, `listen` and `webhooks show` grade every finding as
  a note or an error, and `verify --yours` also says when your signature
  differs only in upper and lower case.

## [0.4.0]

### Added

- Webhook testing without waiting for Paygate. `paygate webhooks send` posts
  the webhook Paygate would send for a payment (or a body from a file) to your
  endpoint, Enhanced and signed with HMAC-SHA256 or Basic. `--tamper
  signature|timestamp|body` breaks it on purpose to check that your endpoint
  rejects it, and `--curl` prints it instead.
- `paygate webhooks verify` checks a received webhook from a saved request or
  a body: the three signature headers, the signature against the HMAC key, the
  timestamp (±5 minutes) and the body against the documented schema, field
  names included. `--yours` takes the signature your own code computed and
  names the usual mistake that produces it, for example the body signed
  without the timestamp.
- `paygate profile set hmac-key` stores the webhook HMAC key of a profile in
  the keychain (asked for hidden, or read from stdin); `PAYGATE_HMAC_KEY`
  overrides it. `paygate profile show` says whether one is set.
- The mock server sends Basic or Enhanced webhooks once a hosted payment
  completes (`-webhook`, `-hmac-key`, `-webhook-to`).

- The app shows the commit next to the version (top border, start screen,
  About), and so does `paygate --version`, so you can tell a fresh build
  apart. Builds with uncommitted changes add `-dirty`, and the install
  script names the commit too.
- The forms show where the API calls go while you choose: the API URL for
  platform and environment, the token URL for OAuth2 (when you add a profile
  and in `paygate init`), and the endpoint of the payment flow under New
  payment.

### Fixed

- The links on the About screen open in the browser when you click them,
  like the checkout URL. A click into the content also moves the keyboard
  there from the menu.
- Adding another profile when one exists no longer skips platform and
  environment after the name. The steps of a form moved the New payment
  form kept in the background, and its answer moved the profile form on.

### Changed

- When you add a profile, the environment choice now reads "Test system" or
  "Production system" ("Testsystem" or "Produktivsystem" in German).
- Hosted payments send `https://webhook.invalid/paygate-cli` as webhook URL
  by default. The `.invalid` domain never resolves, so Paygate's
  notifications reach no server; paygate learns the result by polling as
  before. Profiles that still name the old default
  (`https://paygate.paytest.dev/cli/webhook`) switch to the new one, your own
  webhook URLs stay.

## [0.3.0]

### Added

- Colour themes: Blue (default) or Blurple, under Settings → Colour theme or
  from the command palette.
- Factory reset: `paygate reset` or Settings → Delete all data removes all
  profiles, stored API keys, settings, the local history and the request log.
- Copy any test card number from the command palette (for example type
  "3ds").
- Licensed under the Apache License 2.0, with a notice that paygate is an
  unofficial tool.

### Changed

- Operations use the Paygate API names Capture, Refund and Reversal.
  `paygate cancel` is now `paygate reverse` (`cancel` still works).
- Selected rows are easier to read in all lists, the menu and the command
  palette, also in terminals with 256 colours.
- Card payments offer the test cards in a compact list; the Test cards screen
  made way for the command palette.
- The checkout URL on the wait screen opens with a click; `c` copies it.
- QR codes scan more reliably and fit smaller windows. New app frame.

### Fixed

- Card payments with test cards no longer stop at the authentication step.
- `esc` and `shift+tab` go back from the result and the review.
- The payment page duration on the result no longer keeps counting.
- Smaller layout fixes in forms, the payments list and the command palette.

## [0.2.0]

### Added

- Full-screen app: `paygate` without a command opens it. Sidebar with Home,
  Test cards, Profiles (add, switch, verify, delete) and Settings; footer key
  hints, `?` help, mouse support and tabs in narrow windows. New payment,
  Payments and Inspector follow in the next steps.
- New payment in the app: form, review with request JSON and copy as curl,
  send, result. Hosted pages and 3-D Secure show a QR code and live status;
  results can be sent again, released (manual capture) or refunded.
- Simulation: `--simulate ok|decline|<code>` and a switch in the app send the
  order description `Test:0000` / `Test:0305`, which Paygate treats as a test
  transaction even on production. Production profiles default to simulation,
  0.01 and manual capture.
- Local payment history (`history.jsonl`, IDs, amounts and states only) for
  payments, lookups and follow-up operations.
- Payments in the app: history with filter, lookup by payId or transId,
  details with captured/refunded amounts, timeline, request JSON, and capture,
  refund (full or partial), release and refresh. Home shows today's numbers
  and the latest payments. `paygate history` lists the same on the command
  line (`--all`, `--limit`, `-o json`).
- Inspector in the app: the last 200 requests to Paygate from the app and the
  command line (`requests.jsonl`) with request/response headers and JSON,
  status, timing and correlation ID; copy body or curl, clear the log.
  Credentials are never logged, card numbers and CVC are masked.
- Command palette (`ctrl+k` or `ctrl+p`): fuzzy search over every action in
  English and German: go to screens, look up or open the last payment, copy
  the last pay ID, switch profile, switch language, clear the request log.
- ASCII art: start screen with the large PAYGATE logo (credentials are checked
  meanwhile; can be switched off in Settings or with PAYGATE_NO_SPLASH), small
  logo in the sidebar and results printed as a receipt (d shows the details).
- English and German interface, chosen automatically or in Settings.
- `init` checks test and production, Nexi and Computop, Basic and OAuth2 in
  parallel when the credentials are rejected.

### Fixed

- Direct card payments send `browserInfo.ipAddress`, which Paygate requires
  ("Browser Info Ip Address must not be empty"). It defaults to this computer's
  address and can be set per profile (`customer-ip`, Settings) or with `--ip`.
- Spaces, line breaks and invisible characters around pasted merchant IDs and
  API keys no longer cause 401 errors.

## [0.1.0]

First MVP.

### Added

- `paygate init` and `paygate profile` (list, use, show, set, delete) with API
  keys in the system keychain and credential verification before saving.
- `paygate pay` with an interactive wizard and flags for three flows: hosted
  payment page (`--hosted`), hosted single method (`--method`) and direct card
  payments (`--card`, `--card-number`).
- Live waiting for hosted checkouts and 3-D Secure challenges, with QR code,
  open-in-browser and copy shortcuts.
- `paygate get`, `paygate capture`, `paygate refund` and `paygate cancel` with
  confirmation and automatic amounts.
- `paygate testcards` with aliases for the official test cards.
- Global `-o json`, `--curl`, `-v`/`-vv` with redacted credentials, `--yes` and
  `--no-color`.
- Nexi and Computop hosts, test and production, Basic Auth and OAuth2.
- Local mock Paygate (`tools/mockpaygate`) for demos and tests.
