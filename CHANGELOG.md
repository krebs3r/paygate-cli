# Changelog

All notable changes to this project are documented in this file. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

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
