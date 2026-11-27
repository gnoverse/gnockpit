// Package locationtest builds and signs location documents for tests, as an
// operator's page and gnokey make them.
package locationtest

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/gnolang/gno/tm2/pkg/amino"
	"github.com/gnolang/gno/tm2/pkg/crypto"
	"github.com/gnolang/gno/tm2/pkg/std"

	"github.com/gnoverse/gnockpit/location"
)

// locationBody is a location record's JSON, its keys in the spec's order.
type locationBody struct {
	Chain    string  `json:"chain"`
	Operator string  `json:"operator"`
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
	Country  string  `json:"country"`
	Provider string  `json:"provider"`
	At       string  `json:"at"`
}

// removalBody is a removal record's JSON, its keys in the spec's order.
type removalBody struct {
	Chain    string `json:"chain"`
	Operator string `json:"operator"`
	Clear    bool   `json:"clear"`
	At       string `json:"at"`
}

// Memo is the memo that carries r: MemoPrefix, then r's JSON in the spec's key
// order, a removal when r.Clear is set.
func Memo(r location.Record) string {
	at := r.At.UTC().Format(time.RFC3339)
	var body any = locationBody{
		Chain: r.Chain, Operator: r.Operator, Lat: r.Lat, Lon: r.Lon, Country: r.Country, Provider: r.Provider, At: at,
	}
	if r.Clear {
		body = removalBody{Chain: r.Chain, Operator: r.Operator, Clear: true, At: at}
	}
	return location.MemoPrefix + string(mustMarshal(body))
}

type send struct {
	Type        string `json:"@type"`
	FromAddress string `json:"from_address"`
	ToAddress   string `json:"to_address"`
	Amount      string `json:"amount"`
}

type fee struct {
	GasWanted string `json:"gas_wanted"`
	GasFee    string `json:"gas_fee"`
}

type document struct {
	Msg []send `json:"msg"`
	Fee fee    `json:"fee"`
	// Signatures is left nil: an unsigned document's signatures are null.
	Signatures any    `json:"signatures"`
	Memo       string `json:"memo"`
}

// Document is the unsigned document an operator's page writes for memo: a send
// of 1ugnot from operator to itself, fee gas_wanted 1 and gas_fee 1ugnot, no
// signature (null), as compact JSON.
func Document(operator, memo string) []byte {
	return mustMarshal(document{
		Msg:  []send{{Type: "/bank.MsgSend", FromAddress: operator, ToAddress: operator, Amount: "1ugnot"}},
		Fee:  fee{GasWanted: "1", GasFee: "1ugnot"},
		Memo: memo,
	})
}

// mustMarshal is v's compact JSON with <, > and & written as is, as the page's
// JSON.stringify writes them. v is made of strings, numbers and booleans, so
// encoding fails only on a NaN or infinite number (a record's Lat or Lon),
// which panics.
func mustMarshal(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic(err)
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}

// Sign signs doc with key, as gnokey sign does: over doc's sign bytes for
// chainID, account number 0 and sequence 0 in rendering r, appending the
// signature and the key's public key to doc's signatures.
func Sign(t testing.TB, doc []byte, key crypto.PrivKey, chainID string, r location.Rendering) []byte {
	t.Helper()
	var tx std.Tx
	if err := amino.UnmarshalJSON(doc, &tx); err != nil {
		t.Fatalf("locationtest.Sign: decode %s: %v", doc, err)
	}
	payload, err := location.SignBytes(tx, chainID, r)
	if err != nil {
		t.Fatalf("locationtest.Sign: %v", err)
	}
	sig, err := key.Sign(payload)
	if err != nil {
		t.Fatalf("locationtest.Sign: %v", err)
	}
	tx.Signatures = append(tx.Signatures, std.Signature{PubKey: key.PubKey(), Signature: sig})
	signed, err := amino.MarshalJSON(tx)
	if err != nil {
		t.Fatalf("locationtest.Sign: encode: %v", err)
	}
	return signed
}
