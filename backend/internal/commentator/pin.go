package commentator

import (
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"math/big"
)

const sessionPINLength = 6

func newSessionPIN() (string, error) {
	// 6-digit code, 100000–999999 (no leading zeros).
	n, err := rand.Int(rand.Reader, big.NewInt(900000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()+100000), nil
}

func pinMatches(stored, provided string) bool {
	if stored == "" || provided == "" {
		return false
	}
	if len(stored) != len(provided) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(stored), []byte(provided)) == 1
}
