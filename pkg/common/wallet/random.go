package wallet

import (
	"crypto/rand"
	"math/big"
)

// hutoolBaseChars matches Hutool RandomUtil.BASE_CHAR_NUMBER (lowercase + digits).
const hutoolBaseChars = "abcdefghijklmnopqrstuvwxyz0123456789"

// RandomString generates a random string using Hutool RandomUtil.randomString charset.
func RandomString(length int) (string, error) {
	b := make([]byte, length)
	max := big.NewInt(int64(len(hutoolBaseChars)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = hutoolBaseChars[n.Int64()]
	}
	return string(b), nil
}
