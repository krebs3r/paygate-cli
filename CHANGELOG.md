# Changelog

All notable changes to this project are documented in this file. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- Full-screen app: `paygate` without a command opens it. Sidebar with Home,
  Test cards, Profiles (add, switch, verify, delete) and Settings; footer key
  hints, `?` help, mouse support and tabs in narrow windows. New payment,
  Payments and Inspector follow in the next steps.
- English and German interface, chosen automatically or in Settings.
- `init` checks test and production, Nexi and Computop, Basic and OAuth2 in
  parallel when the credentials are rejected.

### Fixed

- Spaces, line breaks and invisible characters around pasted merchant IDs and
  API keys no longer cause 401 errors.

## [0.1.0] - 2026-10-20

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
