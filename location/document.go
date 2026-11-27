package location

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/gnolang/gno/tm2/pkg/amino"
	"github.com/gnolang/gno/tm2/pkg/crypto"
	"github.com/gnolang/gno/tm2/pkg/crypto/secp256k1"
	"github.com/gnolang/gno/tm2/pkg/std"
)

// SignChainID is the chain id a location document is signed for. No chain
// runs under it, so a signed document is never a transaction a chain accepts.
const SignChainID = "gnockpit-location"

// MaxDocument is the largest signed document accepted, in bytes.
const MaxDocument = 16 << 10

// maxDecodeError bounds how much of amino's decoding error a refusal repeats,
// in runes: the error quotes a type URL, which may fill the document.
const maxDecodeError = 128

// Rendering is a form of a document's sign bytes: gnokey signs one or the
// other depending on its version.
type Rendering int

const (
	// Legacy is the sign bytes of gnokey before v1.5.0, its fee as
	// gas_wanted/gas_fee.
	Legacy Rendering = iota
	// Current is the sign bytes of gnokey v1.5.0 on, its fee as amount/gas.
	Current
)

// sendMsg mirrors gno's bank.MsgSend, so a document's message decodes without
// linking tm2/pkg/sdk/bank and its dependencies. Its amino name, JSON fields
// and std.Msg methods are bank's, so its sign bytes are those of a bank send.
type sendMsg struct {
	FromAddress crypto.Address `json:"from_address" yaml:"from_address"`
	ToAddress   crypto.Address `json:"to_address" yaml:"to_address"`
	Amount      std.Coins      `json:"amount" yaml:"amount"`
}

var _ std.Msg = sendMsg{}

// Registers sendMsg as /bank.MsgSend, the type gnokey names a send.
var _ = amino.RegisterPackage(amino.NewPackage(
	"github.com/gnoverse/gnockpit/location",
	"bank",
	amino.GetCallersDirname(),
).WithDependencies().WithTypes(
	sendMsg{}, "MsgSend",
))

func (msg sendMsg) Route() string { return "bank" }

func (msg sendMsg) Type() string { return "send" }

func (msg sendMsg) ValidateBasic() error {
	if msg.FromAddress.IsZero() {
		return std.ErrInvalidAddress("missing sender address")
	}
	if msg.ToAddress.IsZero() {
		return std.ErrInvalidAddress("missing recipient address")
	}
	if !msg.Amount.IsValid() {
		return std.ErrInvalidCoins(msg.Amount.String())
	}
	if !msg.Amount.IsAllPositive() {
		return std.ErrInsufficientCoins("send amount must be positive")
	}
	return nil
}

func (msg sendMsg) GetSignBytes() []byte {
	return std.MustSortJSON(amino.MustMarshalJSON(msg))
}

func (msg sendMsg) GetSigners() []crypto.Address { return []crypto.Address{msg.FromAddress} }

// signDocFee is the fee as the Current sign bytes render it, mirroring
// gno v1.5.0's tm2/pkg/std signDocFee.
type signDocFee struct {
	Amount []signDocCoin `json:"amount"`
	Gas    string        `json:"gas"`
}

// signDocCoin is a fee coin as the Current sign bytes render it, mirroring
// gno v1.5.0's tm2/pkg/std signDocCoin.
type signDocCoin struct {
	Denom  string `json:"denom"`
	Amount string `json:"amount"`
}

// signDocPayload is the document the Current sign bytes render, mirroring
// gno v1.5.0's tm2/pkg/std signDocPayload.
type signDocPayload struct {
	ChainID       string     `json:"chain_id"`
	AccountNumber uint64     `json:"account_number"`
	Sequence      uint64     `json:"sequence"`
	Fee           signDocFee `json:"fee"`
	Msgs          []std.Msg  `json:"msgs"`
	Memo          string     `json:"memo"`
}

// feeAmount renders the fee coin as gno v1.5.0's feeAmount does: a zero fee is
// an empty list, never nil, since amino renders nil as null and empty as [].
func feeAmount(c std.Coin) []signDocCoin {
	if c.IsZero() {
		return []signDocCoin{}
	}
	return []signDocCoin{{Denom: c.Denom, Amount: strconv.FormatInt(c.Amount, 10)}}
}

// SignBytes returns the bytes a signature of tx for chainID, account number 0
// and sequence 0 is made over, in rendering r.
func SignBytes(tx std.Tx, chainID string, r Rendering) ([]byte, error) {
	switch r {
	case Legacy:
		return tx.GetSignBytes(chainID, 0, 0)
	case Current:
		return currentSignBytes(tx, chainID)
	default:
		return nil, fmt.Errorf("unknown sign bytes rendering %d", r)
	}
}

// currentSignBytes mirrors gno v1.5.0's GetSignaturePayload:
// sortJSON(aminoJSON(signDocPayload)).
func currentSignBytes(tx std.Tx, chainID string) ([]byte, error) {
	data, err := amino.MarshalJSON(signDocPayload{
		ChainID: chainID,
		Fee: signDocFee{
			Amount: feeAmount(tx.Fee.GasFee),
			Gas:    strconv.FormatInt(tx.Fee.GasWanted, 10),
		},
		Msgs: tx.Msgs,
		Memo: tx.Memo,
	})
	if err != nil {
		return nil, fmt.Errorf("unable to marshal sign doc: %w", err)
	}
	return sortJSON(data)
}

// sortJSON mirrors tm2/pkg/std's sortJSON: data through encoding/json as an
// any, which sorts object keys and drops white space.
func sortJSON(data []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("unable to sort payload JSON: %w", err)
	}
	sorted, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("unable to sort payload JSON: %w", err)
	}
	return sorted, nil
}

// Declaration is a verified record and the document that carried it.
type Declaration struct {
	Record
	// Signed is the verified transaction re-encoded by amino: the submitted
	// document's canonical form, which verifies as it does and holds none of
	// its bytes the signature does not cover (white space around it, a field
	// amino ignores, a duplicate key amino reads past).
	Signed string
}

// errNotVerified names the parameters a document must be signed with.
var errNotVerified = errors.New("the signature does not verify: sign with gnokey sign -chainid " +
	SignChainID + " -account-number 0 -account-sequence 0")

// Verify reads a signed location document: an Amino JSON transaction holding
// one message, whose signer is the record's operator, and one signature by the
// operator's secp256k1 key (gnokey's) over its sign bytes for SignChainID,
// account number 0 and sequence 0, in either rendering. The record's form is
// checked by ParseMemo; whether its chain, operator and time suit the request
// is for the caller.
func Verify(doc []byte) (Declaration, error) {
	tx, err := decodeTx(doc)
	if err != nil {
		return Declaration{}, fmt.Errorf("not a transaction document: %s", cut(err.Error(), maxDecodeError))
	}
	if n := len(tx.Msgs); n != 1 {
		return Declaration{}, fmt.Errorf("the document holds %d messages, want 1: a send from the operator to itself", n)
	}
	switch n := len(tx.Signatures); {
	case n == 0:
		return Declaration{}, errors.New("the document holds no signature: sign it with gnokey sign")
	case n > 1:
		return Declaration{}, fmt.Errorf("the document holds %d signatures, want 1", n)
	}
	r, err := ParseMemo(tx.Memo)
	if err != nil {
		return Declaration{}, fmt.Errorf("the record: %w", err)
	}
	send, ok := tx.Msgs[0].(sendMsg)
	if !ok {
		return Declaration{}, errors.New("the document's message is not a send: want a send from the operator to itself")
	}
	if s := send.FromAddress.String(); s != r.Operator {
		return Declaration{}, fmt.Errorf("the message's signer is %s, the record's operator %s", s, r.Operator)
	}
	sig := tx.Signatures[0]
	if sig.PubKey == nil {
		return Declaration{}, errors.New("the signature holds no public key")
	}
	// Only gnokey's key type is accepted: std registers /tm.PubKeyMock in every
	// binary, and a mock key made of the operator's address derives to it and
	// verifies a signature anyone can write.
	if _, ok := sig.PubKey.(secp256k1.PubKeySecp256k1); !ok {
		return Declaration{}, errors.New("the signature's key is not a gnokey secp256k1 key: sign with gnokey sign")
	}
	if a := sig.PubKey.Address().String(); a != r.Operator {
		return Declaration{}, fmt.Errorf("the signature's key is %s's, not the operator's (%s)", a, r.Operator)
	}
	for _, rendering := range []Rendering{Current, Legacy} {
		payload, err := SignBytes(tx, SignChainID, rendering)
		if err != nil {
			return Declaration{}, err
		}
		if sig.PubKey.VerifyBytes(payload, sig.Signature) {
			signed, err := amino.MarshalJSON(tx)
			if err != nil {
				return Declaration{}, fmt.Errorf("could not re-encode the document: %w", err)
			}
			return Declaration{Record: r, Signed: string(signed)}, nil
		}
	}
	return Declaration{}, errNotVerified
}

// decodeTx reads doc as an Amino JSON transaction. amino v1.1.0 panics on some
// malformed input, such as an @type without a slash (typeURLtoFullname), and a
// document is anyone's input: such a panic is returned as the decoding error.
func decodeTx(doc []byte) (tx std.Tx, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("%v", p)
		}
	}()
	err = amino.UnmarshalJSON(doc, &tx)
	return tx, err
}
