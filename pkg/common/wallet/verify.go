package wallet

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/btcsuite/btcd/btcec/v2"
	btcecdsa "github.com/btcsuite/btcd/btcec/v2/ecdsa"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/mr-tron/base58"
)

var ed25519X509Prefix = []byte{0x30, 0x2a, 0x30, 0x05, 0x06, 0x03, 0x2b, 0x65, 0x70, 0x03, 0x21, 0x00}

// CheckEvmAddress mirrors Java UserLoginWalletServiceImpl.checkEvmAddress (msgHash unused).
func CheckEvmAddress(address, str, sign string) bool {
	defer func() { recover() }()
	signBytes, err := decodeHexFlexible(sign)
	if err != nil {
		return false
	}
	msgBytes := []byte(str)
	ethPrefix := fmt.Appendf(nil, "\x19Ethereum Signed Message:\n%d", len(msgBytes))
	fullMsg := append(ethPrefix, msgBytes...)
	messageHash := crypto.Keccak256(fullMsg)

	if len(signBytes) < 64 {
		return false
	}
	r := new(big.Int).SetBytes(signBytes[:32])
	s := new(big.Int).SetBytes(signBytes[32:64])

	for i := 0; i < 4; i++ {
		sig := make([]byte, 65)
		copy(sig[:32], padTo32(r.Bytes()))
		copy(sig[32:64], padTo32(s.Bytes()))
		sig[64] = byte(i)
		pubKey, err := crypto.SigToPub(messageHash, sig)
		if err != nil {
			continue
		}
		recovered := crypto.PubkeyToAddress(*pubKey).Hex()
		if strings.EqualFold(recovered, address) {
			return true
		}
	}
	return false
}

// CheckTronAddress mirrors Java UserLoginWalletServiceImpl.checkTronAddress.
func CheckTronAddress(address, str, sign string) bool {
	defer func() { recover() }()
	signBytes, err := decodeHexFlexible(sign)
	if err != nil {
		return false
	}
	msgBytes := []byte(str)
	if len(signBytes) < 64 {
		return false
	}
	r := new(big.Int).SetBytes(signBytes[:32])
	s := new(big.Int).SetBytes(signBytes[32:64])

	prefixes := []string{
		"\x19TRON Signed Message:\n",
		"\x19Ethereum Signed Message:\n",
	}
	for _, prefix := range prefixes {
		prefixWithLen := fmt.Appendf(nil, "%s%d", prefix, len(msgBytes))
		fullMsg := append(prefixWithLen, msgBytes...)
		messageHash := crypto.Keccak256(fullMsg)
		for i := 0; i < 4; i++ {
			sig := make([]byte, 65)
			copy(sig[:32], padTo32(r.Bytes()))
			copy(sig[32:64], padTo32(s.Bytes()))
			sig[64] = byte(i)
			pubKey, err := crypto.SigToPub(messageHash, sig)
			if err != nil {
				continue
			}
			recovered := publicKeyToTronAddress(pubKey)
			if recovered == address {
				return true
			}
		}
	}
	return false
}

func publicKeyToTronAddress(pubKey *ecdsa.PublicKey) string {
	addr := crypto.PubkeyToAddress(*pubKey)
	ethAddrBytes := addr.Bytes()
	addressBytes := make([]byte, 21)
	addressBytes[0] = 0x41
	copy(addressBytes[1:], ethAddrBytes)
	hash1 := sha256.Sum256(addressBytes)
	hash2 := sha256.Sum256(hash1[:])
	checksum := hash2[:4]
	tronBytes := append(addressBytes, checksum...)
	return base58.Encode(tronBytes)
}

// CheckBitcoinAddress mirrors Java bitcoinj signedMessageToKey + multi-format compare.
func CheckBitcoinAddress(address, str, sign string) bool {
	defer func() { recover() }()
	pubKey, err := signedMessageToKey(str, sign)
	if err != nil {
		return false
	}
	for _, pubBytes := range [][]byte{pubKey.SerializeCompressed(), pubKey.SerializeUncompressed()} {
		hash160 := btcutil.Hash160(pubBytes)
		if legacyMainnet, err := btcutil.NewAddressPubKeyHash(hash160, &chaincfg.MainNetParams); err == nil && legacyMainnet.EncodeAddress() == address {
			return true
		}
		if legacyTestnet, err := btcutil.NewAddressPubKeyHash(hash160, &chaincfg.TestNet3Params); err == nil && legacyTestnet.EncodeAddress() == address {
			return true
		}
	}
	compressedHash := btcutil.Hash160(pubKey.SerializeCompressed())
	if segwit, err := btcutil.NewAddressWitnessPubKeyHash(compressedHash, &chaincfg.MainNetParams); err == nil && segwit.EncodeAddress() == address {
		return true
	}
	if segwit, err := btcutil.NewAddressWitnessPubKeyHash(compressedHash, &chaincfg.TestNet3Params); err == nil && segwit.EncodeAddress() == address {
		return true
	}
	return false
}

func signedMessageToKey(message, signatureBase64 string) (*btcec.PublicKey, error) {
	sigBytes, err := base64.StdEncoding.DecodeString(signatureBase64)
	if err != nil {
		return nil, err
	}
	if len(sigBytes) != 65 {
		return nil, fmt.Errorf("invalid signature length")
	}
	header := int(sigBytes[0]) & 0xff
	if header < 27 || header > 34 {
		return nil, fmt.Errorf("invalid recovery header")
	}
	recID := header - 27
	if recID >= 4 {
		recID -= 4
	}

	hash := bitcoinMessageHash(message)
	compactSig := make([]byte, 65)
	compactSig[0] = byte(27 + recID)
	copy(compactSig[1:], sigBytes[1:])
	pubKey, _, err := btcecdsa.RecoverCompact(compactSig, hash)
	if err != nil {
		return nil, err
	}
	return pubKey, nil
}

func bitcoinMessageHash(message string) []byte {
	const header = "Bitcoin Signed Message:\n"
	msgBytes := []byte(message)
	buf := make([]byte, 0, 1+len(header)+9+len(msgBytes))
	buf = append(buf, byte(len(header)))
	buf = append(buf, header...)
	buf = append(buf, encodeVarInt(uint64(len(msgBytes)))...)
	buf = append(buf, msgBytes...)
	first := sha256.Sum256(buf)
	second := sha256.Sum256(first[:])
	return second[:]
}

func encodeVarInt(n uint64) []byte {
	switch {
	case n < 0xfd:
		return []byte{byte(n)}
	case n <= 0xffff:
		return []byte{0xfd, byte(n), byte(n >> 8)}
	case n <= 0xffffffff:
		return []byte{0xfe, byte(n), byte(n >> 8), byte(n >> 16), byte(n >> 24)}
	default:
		return []byte{0xff, byte(n), byte(n >> 8), byte(n >> 16), byte(n >> 24), byte(n >> 32), byte(n >> 40), byte(n >> 48), byte(n >> 56)}
	}
}

// CheckSolanaAddress mirrors Java UserLoginWalletServiceImpl.checkSolanaAddress.
func CheckSolanaAddress(address, str, msgHash, sign string) bool {
	defer func() { recover() }()
	pubKeyBytes, err := base58.Decode(address)
	if err != nil {
		return false
	}
	x509PubKey := make([]byte, len(ed25519X509Prefix)+len(pubKeyBytes))
	copy(x509PubKey, ed25519X509Prefix)
	copy(x509PubKey[len(ed25519X509Prefix):], pubKeyBytes)

	var messageBytes []byte
	if msgHash != "" {
		messageBytes, err = hex.DecodeString(strings.TrimPrefix(msgHash, "0x"))
		if err != nil {
			return false
		}
	} else {
		messageBytes = []byte(str)
	}
	signatureBytes, err := hex.DecodeString(strings.TrimPrefix(sign, "0x"))
	if err != nil {
		return false
	}
	if len(pubKeyBytes) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(pubKeyBytes), messageBytes, signatureBytes)
}

func padTo32(b []byte) []byte {
	if len(b) >= 32 {
		return b[len(b)-32:]
	}
	out := make([]byte, 32)
	copy(out[32-len(b):], b)
	return out
}

func decodeHexFlexible(s string) ([]byte, error) {
	if s == "" {
		return nil, fmt.Errorf("empty hex")
	}
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		return hexutil.Decode(s)
	}
	return hex.DecodeString(s)
}

// ChainAddresses holds verified addresses (empty string = chain not provided).
type ChainAddresses struct {
	Evm     string
	Tron    string
	Bitcoin string
	Solana  string
}

// ChainParams mirrors Java AppLoginChainParams for verification input.
type ChainParams struct {
	Address string
	Sign    string
	MsgHash string
}

func HasAnyChain(evm, tron, bitcoin, solana *ChainParams) bool {
	return evm != nil || tron != nil || bitcoin != nil || solana != nil
}

// VerifyChains mirrors AppWalletController validation + validChainAddress logic.
func VerifyChains(str string, evm, tron, bitcoin, solana *ChainParams) (ChainAddresses, error) {
	if !HasAnyChain(evm, tron, bitcoin, solana) {
		return ChainAddresses{}, ErrLeastTransmit
	}
	evmAddr, err := validChainAddress(evm, func() bool {
		return CheckEvmAddress(evm.Address, str, evm.Sign)
	})
	if err != nil {
		return ChainAddresses{}, err
	}
	tronAddr, err := validChainAddress(tron, func() bool {
		return CheckTronAddress(tron.Address, str, tron.Sign)
	})
	if err != nil {
		return ChainAddresses{}, err
	}
	bitcoinAddr, err := validChainAddress(bitcoin, func() bool {
		return CheckBitcoinAddress(bitcoin.Address, str, bitcoin.Sign)
	})
	if err != nil {
		return ChainAddresses{}, err
	}
	solanaAddr, err := validChainAddress(solana, func() bool {
		return CheckSolanaAddress(solana.Address, str, solana.MsgHash, solana.Sign)
	})
	if err != nil {
		return ChainAddresses{}, err
	}
	return ChainAddresses{
		Evm:     evmAddr,
		Tron:    tronAddr,
		Bitcoin: bitcoinAddr,
		Solana:  solanaAddr,
	}, nil
}

func validChainAddress(chain *ChainParams, checker func() bool) (string, error) {
	if chain == nil {
		return "", nil
	}
	if checker() {
		return chain.Address, nil
	}
	return "", ErrWalletAddressCheck
}
