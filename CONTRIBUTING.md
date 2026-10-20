# Contributing

Thanks for helping to improve Paygate CLI.

## License of contributions

Paygate CLI is licensed under the [Apache License 2.0](LICENSE). By opening a
pull request you agree that your contribution is licensed under the same terms
(section 5 of the license). Please keep the existing copyright and the
[NOTICE](NOTICE) file intact.

## Before you open a pull request

1. Build and test:

   ```bash
   make test        # all tests with the race detector
   make vet
   ```

2. Try your change against the local mock instead of real merchants:

   ```bash
   make mock                                       # terminal 1
   export PAYGATE_BASE_URL=http://127.0.0.1:8089   # terminal 2
   paygate init --name mock --merchant-id demo-shop --api-key demo-key
   ```

3. Keep user-facing texts in English and German (`internal/i18n/messages.go`).

4. If you add or update a dependency, regenerate the third-party notices:

   ```bash
   make notices
   ```

## Never include secrets

Do not paste API keys, merchant credentials, card numbers or real payment data
into issues, pull requests, tests or screenshots. Use the mock server and the
test cards from `paygate testcards`.
