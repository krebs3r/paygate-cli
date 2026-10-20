// Command mockpaygate runs a local imitation of the Paygate REST API v2 for
// demos and manual testing without merchant credentials:
//
//	go run ./tools/mockpaygate
//	PAYGATE_BASE_URL=http://127.0.0.1:8089 paygate pay
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
	flag.Parse()

	s := mockpaygate.New(*mid, *key)
	s.BaseURL = "http://" + *addr
	s.NotFoundPolls, s.PendingPolls = *notFound, *pending

	fmt.Printf("Mock Paygate listening on %s\n\n", s.BaseURL)
	fmt.Printf("  export PAYGATE_BASE_URL=%s\n", s.BaseURL)
	fmt.Printf("  paygate init --name mock --merchant-id %s --api-key %s\n\n", *mid, *key)
	log.Fatal(http.ListenAndServe(*addr, s.Handler()))
}
