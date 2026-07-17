package wallet

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/mr-tron/base58"
)

func TestCheckEvmAddress_PersonalSign(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	address := crypto.PubkeyToAddress(key.PublicKey).Hex()
	str := "3xbly4t54i"

	msg := []byte(str)
	prefix := []byte(fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(msg)))
	hash := crypto.Keccak256(append(prefix, msg...))
	sig, err := crypto.Sign(hash, key)
	if err != nil {
		t.Fatal(err)
	}
	signHex := hexutil.Encode(sig)

	if !CheckEvmAddress(address, str, signHex) {
		t.Fatalf("expected valid signature for %s", address)
	}
	if CheckEvmAddress("0x0000000000000000000000000000000000000001", str, signHex) {
		t.Fatal("expected mismatch address to fail")
	}
}

func TestCheckSolanaAddress_UTF8Message(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	str := "helloworld"
	sig := ed25519.Sign(priv, []byte(str))
	addr := base58.Encode(pub)
	signHex := hex.EncodeToString(sig)
	if !CheckSolanaAddress(addr, str, "", signHex) {
		t.Fatal("expected solana verify to pass")
	}
	if !CheckSolanaAddress(addr, str, hex.EncodeToString([]byte(str)), signHex) {
		t.Fatal("expected solana verify with msgHash to pass")
	}
}

func TestRandomString_Charset(t *testing.T) {
	s, err := RandomString(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 10 {
		t.Fatalf("len=%d", len(s))
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
			t.Fatalf("unexpected char %c", c)
		}
	}
}

func TestVerifyChains_Empty(t *testing.T) {
	_, err := VerifyChains("abc", nil, nil, nil, nil)
	if err != ErrLeastTransmit {
		t.Fatalf("got %v", err)
	}
}
