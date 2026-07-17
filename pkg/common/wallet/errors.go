package wallet

// ErrKey is a wallet business error key aligned with Java R.fail(msg).
type ErrKey string

func (e ErrKey) Error() string { return string(e) }

const (
	ErrSignKey            ErrKey = "sign.key.error"
	ErrLeastTransmit      ErrKey = "least.transmit.one.piece.of.data"
	ErrWalletAddressCheck ErrKey = "wallet.address.check.error"
	ErrCommonFail         ErrKey = "common.fail"
)
