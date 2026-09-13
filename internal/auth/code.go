// Package auth implements device pairing: setup/pairing code generation,
// token issuance, and pairing-attempt rate limiting (§6).
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"math/big"
)

// setupCodeAlphabet is alphanumeric only, per §6.1 ("not a short human-typed
// PIN"): upper, lower, and digits.
const setupCodeAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// setupCodeLength is chosen for headroom over §6.1's "at least 8
// alphanumeric characters (≈41+ bits of entropy)" floor: log2(62^8) ≈ 47.6
// bits already clears that bar, and 12 characters leaves extra margin.
const setupCodeLength = 12

// GenerateSetupCode returns a fresh random bootstrap setup code (§6.1).
func GenerateSetupCode() (string, error) {
	return randomString(setupCodeAlphabet, setupCodeLength)
}

// pairingCodeDigits is the alphabet for the short-lived §6.2 pairing code — a
// human types this into an already-trusted device's approval prompt (spec
// example: "4821"), so unlike the bootstrap code it favors being easy to
// read and key in over raw entropy; the 10-minute expiry (§6.2) plus the
// §6.3 lockout are what actually keep it safe from guessing.
const pairingCodeDigits = "0123456789"

// pairingCodeLength matches the 4-digit example in §6.2 exactly.
const pairingCodeLength = 4

// GeneratePairingCode returns a fresh random pairing code for the §6.2
// subsequent-device flow.
func GeneratePairingCode() (string, error) {
	return randomString(pairingCodeDigits, pairingCodeLength)
}

// tokenBytes is the raw entropy (256 bits) behind a long-lived device
// token (§6.2) before base64url encoding.
const tokenBytes = 32

// GenerateToken returns a fresh random long-lived device token (§6.2),
// issued once at pairing time and never recoverable from devices.token_hash
// (§5.2) afterwards.
func GenerateToken() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashToken returns the value stored in devices.token_hash (§5.2) for a raw
// token. A plain SHA-256 digest is sufficient here — unlike a user-chosen
// password, the input already carries 256 bits of its own entropy, so it
// isn't practically guessable or worth the cost of a slow KDF.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ConstantTimeEqual reports whether a and b are equal without leaking
// timing information about their length or where they first differ —
// hashing both sides first means even a and b of different lengths compare
// in constant time. Used to check a presented setup/pairing code (§6.1,
// §6.3) against the real one.
func ConstantTimeEqual(a, b string) bool {
	ah := sha256.Sum256([]byte(a))
	bh := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ah[:], bh[:]) == 1
}

func randomString(alphabet string, n int) (string, error) {
	max := big.NewInt(int64(len(alphabet)))
	out := make([]byte, n)
	for i := range out {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = alphabet[idx.Int64()]
	}
	return string(out), nil
}
