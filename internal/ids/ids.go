// Package ids generates transaction IDs and idempotency keys.
package ids

import (
	"crypto/rand"
	"encoding/hex"
	"math/big"
	"strconv"
	"strings"
	"time"
)

const alnum = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"

// NewTransID returns a unique, purely alphanumeric transaction ID of at most
// 24 characters. The stricter iDEAL rules ([a-zA-Z0-9], max. 24) are the
// lowest common denominator, so the ID works for every payment method.
func NewTransID() string {
	ts := strings.ToUpper(strconv.FormatInt(time.Now().UnixMilli(), 36))
	return "TID" + ts + randomAlnum(6)
}

// NewIdempotencyKey returns a random key for follow-up operations, which must
// not reuse the payment's transId.
func NewIdempotencyKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func randomAlnum(n int) string {
	var sb strings.Builder
	max := big.NewInt(int64(len(alnum)))
	for i := 0; i < n; i++ {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			sb.WriteByte('X')
			continue
		}
		sb.WriteByte(alnum[v.Int64()])
	}
	return sb.String()
}
