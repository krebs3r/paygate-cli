// Command mockpaygate runs a local imitation of the Paygate REST API v2 for
// demos and manual testing without merchant credentials:
//
//	go run ./tools/mockpaygate
//	PAYGATE_BASE_URL=http://127.0.0.1:8089 paygate pay
//
// With -webhook basic or -webhook enhanced, completed hosted payments post a
// webhook to their urls.webhook, or to -webhook-to; enhanced ones are signed
// with -hmac-key.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"github.com/krebs3r/paygate-cli/internal/mockpaygate"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8089", "listen address")
	mid := flag.String("merchant-id", "demo-shop", "accepted merchant ID")
	key := flag.String("api-key", "demo-key", "accepted API key")
	notFound := flag.Int("not-found-polls", 2, "hosted payments: status lookups answered with 404 first")
	pending := flag.Int("pending-polls", 2, "hosted payments: then answered as pending")
	webhook := flag.String("webhook", "off", "webhook of completed hosted payments: off, basic or enhanced")
	hmacKey := flag.String("hmac-key", "demo-hmac-key", "HMAC key that signs enhanced webhooks")
	webhookTo := flag.String("webhook-to", "", "send webhooks here instead of urls.webhook, e.g. http://localhost:3000/webhook")
	flag.Parse()

	s := mockpaygate.New(*mid, *key)
	s.BaseURL = "http://" + *addr
	s.NotFoundPolls, s.PendingPolls = *notFound, *pending
	switch *webhook {
	case "off", "basic", "enhanced":
	default:
		log.Fatalf("unknown -webhook %q (use off, basic or enhanced)", *webhook)
	}
	s.Webhook, s.HMACKey, s.WebhookTo = *webhook, *hmacKey, *webhookTo
	s.OnWebhook = func(url string, status int, err error) {
		if err != nil {
			log.Printf("webhook to %s failed: %v", url, err)
			return
		}
		log.Printf("webhook to %s: %d", url, status)
	}

	fmt.Printf("Mock Paygate listening on %s\n\n", s.BaseURL)
	fmt.Printf("  export PAYGATE_BASE_URL=%s\n", s.BaseURL)
	fmt.Printf("  paygate init --name mock --merchant-id %s --api-key %s\n", *mid, *key)
	if *webhook != "off" {
		if *webhookTo != "" {
			fmt.Printf("\n  %s webhooks of hosted payments go to %s\n", *webhook, *webhookTo)
		} else {
			fmt.Printf("  paygate profile set webhook-url https://…   # %s webhooks after hosted payments\n", *webhook)
		}
		if *webhook == "enhanced" {
			fmt.Printf("  paygate profile set hmac-key %s\n", *hmacKey)
		}
	}
	fmt.Println()
	log.Fatal(http.ListenAndServe(*addr, s.Handler()))
}
