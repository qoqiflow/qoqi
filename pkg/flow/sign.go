package flow

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SignatureHeader carries the signature of every request the daemon
// sends a queue.
const SignatureHeader = "X-Flow-Signature"

// Skew is how far from the receiver's clock a signature's moment may be:
// a request recorded is not played again later.
const Skew = 5 * time.Minute

// Sign is the signature of a request to a queue: the moment, the method,
// the path, the queue, then the body, each on a line of its own, with
// the queue's secret, as the header writes it: "t=<ms>, v1=<hex>".
func Sign(secret string, at time.Time, method, path, queue string, body []byte) string {
	return fmt.Sprintf("t=%d, v1=%s", at.UnixMilli(), mac(secret, at.UnixMilli(), method, path, queue, body))
}

func mac(secret string, at int64, method, path, queue string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(m, "%d\n%s\n%s\n%s\n", at, method, path, queue)
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// ErrSignature is a request whose signature is wrong, missing, or too
// old.
var ErrSignature = errors.New("the request is not signed by the daemon")

// Verify checks the signature of a request to a queue, with any of its
// secrets: while one is being changed, the daemon signs with both, and
// the header has a v1= for each.
func Verify(header string, now time.Time, method, path, queue string, body []byte, secrets ...string) error {
	var at int64 = -1
	var given []string
	for _, part := range strings.Split(header, ",") {
		key, value, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch key {
		case "t":
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return fmt.Errorf("%w: its moment is not a number", ErrSignature)
			}
			at = n
		case "v1":
			given = append(given, value)
		}
	}
	if at < 0 || len(given) == 0 {
		return fmt.Errorf("%w: %s is missing or not t=..., v1=...", ErrSignature, SignatureHeader)
	}
	if d := now.Sub(time.UnixMilli(at)); d > Skew || d < -Skew {
		return fmt.Errorf("%w: it was signed %v from this clock, more than %v", ErrSignature, d.Round(time.Second), Skew)
	}
	for _, secret := range secrets {
		want := mac(secret, at, method, path, queue, body)
		for _, g := range given {
			if hmac.Equal([]byte(g), []byte(want)) {
				return nil
			}
		}
	}
	return ErrSignature
}
