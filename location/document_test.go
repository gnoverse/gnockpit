package location_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gnolang/gno/tm2/pkg/amino"
	"github.com/gnolang/gno/tm2/pkg/crypto"
	"github.com/gnolang/gno/tm2/pkg/crypto/mock"
	"github.com/gnolang/gno/tm2/pkg/crypto/secp256k1"
	"github.com/gnolang/gno/tm2/pkg/std"

	"github.com/gnoverse/gnockpit/location"
	"github.com/gnoverse/gnockpit/location/locationtest"
)

// gnokeyOperator is the address of gno's public test mnemonic, the key the
// documents in testdata were signed with by real gnokeys.
const gnokeyOperator = "g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5"

const (
	unsigned    = "the document holds no signature: sign it with gnokey sign"
	notVerified = "the signature does not verify: sign with gnokey sign -chainid gnockpit-location -account-number 0 -account-sequence 0"
)

var renderings = []struct {
	name string
	r    location.Rendering
}{
	{"Legacy", location.Legacy},
	{"Current", location.Current},
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	doc, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func decode(t *testing.T, doc []byte) std.Tx {
	t.Helper()
	var tx std.Tx
	if err := amino.UnmarshalJSON(doc, &tx); err != nil {
		t.Fatalf("amino.UnmarshalJSON(%s): %v", doc, err)
	}
	return tx
}

func encode(t *testing.T, tx std.Tx) []byte {
	t.Helper()
	doc, err := amino.MarshalJSON(tx)
	if err != nil {
		t.Fatalf("amino.MarshalJSON: %v", err)
	}
	return doc
}

func key(name string) crypto.PrivKey { return secp256k1.GenPrivKeySecp256k1([]byte(name)) }

func address(k crypto.PrivKey) string { return k.PubKey().Address().String() }

// record is operator's location, as the test documents declare it.
func record(operator string) location.Record {
	return location.Record{
		Chain: "gnoland1", Operator: operator, Lat: 1.3, Lon: 103.8, Country: "SG", Provider: "OVH SAS",
		At: time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC),
	}
}

// unsignedDocument is k's own location in a send from k to itself.
func unsignedDocument(k crypto.PrivKey) []byte {
	op := address(k)
	return locationtest.Document(op, locationtest.Memo(record(op)))
}

func verifyFails(t *testing.T, doc []byte, want string) {
	t.Helper()
	_, err := location.Verify(doc)
	if err == nil || err.Error() != want {
		t.Fatalf("Verify(%s) = %v, want %q", doc, err, want)
	}
}

// verifiesOver reports whether tx's one signature verifies over its sign bytes
// for SignChainID in rendering r.
func verifiesOver(t *testing.T, tx std.Tx, r location.Rendering) bool {
	t.Helper()
	if len(tx.Signatures) != 1 {
		t.Fatalf("the document holds %d signatures, want 1", len(tx.Signatures))
	}
	payload, err := location.SignBytes(tx, location.SignChainID, r)
	if err != nil {
		t.Fatalf("SignBytes(tx, SignChainID, %d): %v", r, err)
	}
	sig := tx.Signatures[0]
	return sig.PubKey.VerifyBytes(payload, sig.Signature)
}

// fixtureRecord is the record the documents in testdata declare.
var fixtureRecord = location.Record{
	Chain: "gnoland1", Operator: gnokeyOperator, Lat: 48.9, Lon: 2.4, Country: "FR", Provider: "AT&T",
	At: time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC),
}

func TestMemoIsTheSignedForm(t *testing.T) {
	signed := decode(t, readFixture(t, "gnokey-v1.1.0.json")).Memo
	if got := locationtest.Memo(fixtureRecord); got != signed {
		t.Errorf("Memo(%+v) = %s, want the memo gnokey signed %s", fixtureRecord, got, signed)
	}
	removal := fixtureRecord
	removal.Clear = true
	want := `gnockpit-location/v1 {"chain":"gnoland1","operator":"` + gnokeyOperator + `","clear":true,"at":"2026-11-26T12:00:00Z"}`
	if got := locationtest.Memo(removal); got != want {
		t.Errorf("Memo(removal) = %s, want %s", got, want)
	}
}

func TestVerifyGnokeySignatures(t *testing.T) {
	for _, name := range []string{"gnokey-v1.1.0.json", "gnokey-v1.7.1.json"} {
		t.Run(name, func(t *testing.T) {
			doc := readFixture(t, name)
			got, err := location.Verify(doc)
			if err != nil {
				t.Fatalf("Verify(%s): %v", name, err)
			}
			if got.Record != fixtureRecord {
				t.Errorf("Verify(%s).Record = %+v, want %+v", name, got.Record, fixtureRecord)
			}
			checkSigned(t, got, doc)
		})
	}
}

// checkSigned checks that d, verified from doc, holds doc's transaction
// re-encoded by amino, and that this canonical form verifies to d's record.
func checkSigned(t *testing.T, d location.Declaration, doc []byte) {
	t.Helper()
	if want := string(encode(t, decode(t, doc))); d.Signed != want {
		t.Errorf("Verify(%s).Signed = %s, want the transaction re-encoded %s", doc, d.Signed, want)
	}
	again, err := location.Verify([]byte(d.Signed))
	if err != nil {
		t.Fatalf("Verify(Signed %s): %v", d.Signed, err)
	}
	if again.Record != d.Record || again.Signed != d.Signed {
		t.Errorf("Verify(Signed) = %+v, want %+v", again, d)
	}
}

func TestVerifySignedHoldsNoUnsignedByte(t *testing.T) {
	fixture := readFixture(t, "gnokey-v1.7.1.json")
	// amino ignores an unknown field, and of two "memo" keys the last is read:
	// neither the field nor the shadowed memo is covered by the signature.
	doc := bytes.Replace(fixture, []byte(`{"msg"`), []byte(`{"memo":"shadowed","extra":"unsigned","msg"`), 1)
	got, err := location.Verify(doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, unsigned := range []string{"shadowed", "extra"} {
		if strings.Contains(got.Signed, unsigned) {
			t.Errorf("Verify(...).Signed = %s, holding the unsigned %q", got.Signed, unsigned)
		}
	}
	checkSigned(t, got, doc)
}

func TestFixtureRenderings(t *testing.T) {
	for _, tc := range []struct {
		name            string
		verifies, fails location.Rendering
	}{
		{"gnokey-v1.1.0.json", location.Legacy, location.Current},
		{"gnokey-v1.7.1.json", location.Current, location.Legacy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := decode(t, readFixture(t, tc.name))
			if !verifiesOver(t, tx, tc.verifies) {
				t.Errorf("%s's signature does not verify over rendering %d", tc.name, tc.verifies)
			}
			if verifiesOver(t, tx, tc.fails) {
				t.Errorf("%s's signature verifies over rendering %d", tc.name, tc.fails)
			}
		})
	}
}

func TestSendMsgMirrorsBank(t *testing.T) {
	const (
		op   = gnokeyOperator
		zero = "g1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqluuxe"
	)
	// The sign bytes and ValidateBasic errors are those gno v1.1.0's
	// bank.MsgSend gives for the same messages.
	for _, tc := range []struct {
		name, from, to, amount, signBytes, invalid string
	}{
		{"a send", op, op, "1ugnot", `{"amount":"1ugnot","from_address":"` + op + `","to_address":"` + op + `"}`, ""},
		{"no sender", "", op, "1ugnot", `{"amount":"1ugnot","from_address":"` + zero + `","to_address":"` + op + `"}`, "invalid address error"},
		{"no recipient", op, "", "1ugnot", `{"amount":"1ugnot","from_address":"` + op + `","to_address":"` + zero + `"}`, "invalid address error"},
		{"no amount", op, op, "", `{"amount":"","from_address":"` + op + `","to_address":"` + op + `"}`, "insufficient coins error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := fmt.Sprintf(`{"@type":"/bank.MsgSend","from_address":%q,"to_address":%q,"amount":%q}`, tc.from, tc.to, tc.amount)
			var msg std.Msg
			if err := amino.UnmarshalJSON([]byte(m), &msg); err != nil {
				t.Fatalf("amino.UnmarshalJSON(%s): %v", m, err)
			}
			if msg.Route() != "bank" || msg.Type() != "send" {
				t.Errorf("Route(), Type() = %q, %q, want bank, send", msg.Route(), msg.Type())
			}
			if got := string(msg.GetSignBytes()); got != tc.signBytes {
				t.Errorf("GetSignBytes() = %s, want %s", got, tc.signBytes)
			}
			signer := tc.from
			if signer == "" {
				signer = zero
			}
			if got := msg.GetSigners(); len(got) != 1 || got[0].String() != signer {
				t.Errorf("GetSigners() = %v, want [%s], the sender", got, signer)
			}
			invalid := ""
			if err := msg.ValidateBasic(); err != nil {
				invalid = err.Error()
			}
			if invalid != tc.invalid {
				t.Errorf("ValidateBasic() = %q, want %q (empty: no error)", invalid, tc.invalid)
			}
		})
	}
}

func TestCurrentRenderingOfAFee(t *testing.T) {
	tx := decode(t, locationtest.Document(gnokeyOperator, locationtest.Memo(fixtureRecord)))
	tx.Fee = std.Fee{GasWanted: 7, GasFee: std.Coin{Denom: "ugnot", Amount: 3}}
	// gno v1.7.1's tx.GetSignBytes(SignChainID, 0, 0) for the same transaction:
	// gas 7, a fee of 3ugnot and a send of 1ugnot, each in its place.
	const want = `{"account_number":"0","chain_id":"gnockpit-location","fee":{"amount":[{"amount":"3","denom":"ugnot"}],"gas":"7"},"memo":"gnockpit-location/v1 {\"chain\":\"gnoland1\",\"operator\":\"g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5\",\"lat\":48.9,\"lon\":2.4,\"country\":\"FR\",\"provider\":\"AT\u0026T\",\"at\":\"2026-11-26T12:00:00Z\"}","msgs":[{"@type":"/bank.MsgSend","amount":"1ugnot","from_address":"g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5","to_address":"g1jg8mtutu9khhfwc4nxmuhcpftf0pajdhfvsqf5"}],"sequence":"0"}`
	got, err := location.SignBytes(tx, location.SignChainID, location.Current)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("SignBytes(gas 7, fee 3ugnot, Current) = %s, want %s", got, want)
	}
}

func TestCurrentRenderingOfAZeroFee(t *testing.T) {
	tx := decode(t, unsignedDocument(key("a")))
	tx.Fee.GasFee = std.Coin{}
	payload, err := location.SignBytes(tx, location.SignChainID, location.Current)
	if err != nil {
		t.Fatal(err)
	}
	if want := `"fee":{"amount":[],"gas":"1"}`; !bytes.Contains(payload, []byte(want)) {
		t.Fatalf("SignBytes(zero fee, Current) = %s, want it to hold %s", payload, want)
	}
}

func TestSignBytesRefusesAnUnknownRendering(t *testing.T) {
	tx := decode(t, unsignedDocument(key("a")))
	if _, err := location.SignBytes(tx, location.SignChainID, location.Rendering(2)); err == nil {
		t.Fatal("SignBytes(tx, SignChainID, 2) = nil error, want an unknown rendering refused")
	}
}

func TestVerifyRefusesAnUnsignedDocument(t *testing.T) {
	verifyFails(t, unsignedDocument(key("a")), unsigned)
}

func TestVerifyNamesTheSignParameters(t *testing.T) {
	k := key("a")
	doc := unsignedDocument(k)
	for _, rc := range renderings {
		t.Run("chain id dev, "+rc.name, func(t *testing.T) {
			verifyFails(t, locationtest.Sign(t, doc, k, "dev", rc.r), notVerified)
		})
	}
	for _, tc := range []struct {
		name         string
		account, seq uint64
	}{
		{"account number 1", 1, 0},
		{"sequence 1", 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := decode(t, doc)
			payload, err := tx.GetSignBytes(location.SignChainID, tc.account, tc.seq)
			if err != nil {
				t.Fatal(err)
			}
			sig, err := k.Sign(payload)
			if err != nil {
				t.Fatal(err)
			}
			tx.Signatures = []std.Signature{{PubKey: k.PubKey(), Signature: sig}}
			verifyFails(t, encode(t, tx), notVerified)
		})
	}
}

func TestVerifyRefusesATamperedRecord(t *testing.T) {
	doc := readFixture(t, "gnokey-v1.1.0.json")
	if n := bytes.Count(doc, []byte("48.9")); n != 1 {
		t.Fatalf("the fixture holds 48.9 %d times, want once", n)
	}
	verifyFails(t, bytes.Replace(doc, []byte("48.9"), []byte("49.9"), 1), notVerified)
}

func TestVerifyRefusesAnotherSigner(t *testing.T) {
	a, b := key("a"), key("b")
	memoA := locationtest.Memo(record(address(a)))
	t.Run("a send from another", func(t *testing.T) {
		doc := locationtest.Sign(t, locationtest.Document(address(b), memoA), b, location.SignChainID, location.Legacy)
		verifyFails(t, doc, fmt.Sprintf("the message's signer is %s, the record's operator %s", address(b), address(a)))
	})
	t.Run("a send from another to the operator, signed by the operator", func(t *testing.T) {
		fromA := `"from_address":"` + address(a) + `"`
		toA := bytes.Replace(locationtest.Document(address(a), memoA), []byte(fromA), []byte(`"from_address":"`+address(b)+`"`), 1)
		doc := locationtest.Sign(t, toA, a, location.SignChainID, location.Legacy)
		verifyFails(t, doc, fmt.Sprintf("the message's signer is %s, the record's operator %s", address(b), address(a)))
	})
	t.Run("signed by another", func(t *testing.T) {
		doc := locationtest.Sign(t, locationtest.Document(address(a), memoA), b, location.SignChainID, location.Legacy)
		verifyFails(t, doc, fmt.Sprintf("the signature's key is %s's, not the operator's (%s)", address(b), address(a)))
	})
}

func TestVerifyRefusesANullMessage(t *testing.T) {
	k := key("a")
	tx := decode(t, unsignedDocument(k))
	tx.Msgs = []std.Msg{nil}
	doc := locationtest.Sign(t, encode(t, tx), k, location.SignChainID, location.Legacy)
	if !bytes.Contains(doc, []byte(`"msg":[null]`)) {
		t.Fatalf("the document %s holds no null message", doc)
	}
	verifyFails(t, doc, "the document's message is not a send: want a send from the operator to itself")
}

func TestVerifyRefusesAKeyGnokeyDoesNotSignWith(t *testing.T) {
	const want = "the signature's key is not a gnokey secp256k1 key: sign with gnokey sign"
	k := key("a")
	op := k.PubKey().Address()
	// std registers /tm.PubKeyMock in every binary. A mock key made of the
	// operator's public address derives to it and signs with a text anyone writes.
	forger := mock.PrivKeyMock(op[:])
	for _, rc := range renderings {
		t.Run("a mock key holding the operator's address, "+rc.name, func(t *testing.T) {
			verifyFails(t, locationtest.Sign(t, unsignedDocument(k), forger, location.SignChainID, rc.r), want)
		})
	}
	t.Run("a mock key longer than an address", func(t *testing.T) {
		long := mock.PrivKeyMock(make([]byte, len(op)+1))
		verifyFails(t, locationtest.Sign(t, unsignedDocument(k), long, location.SignChainID, location.Legacy), want)
	})
}

func TestVerifyRefusesASignatureWithoutKey(t *testing.T) {
	k := key("a")
	tx := decode(t, locationtest.Sign(t, unsignedDocument(k), k, location.SignChainID, location.Legacy))
	tx.Signatures[0].PubKey = nil
	verifyFails(t, encode(t, tx), "the signature holds no public key")
}

func TestVerifyRefusesABadRecord(t *testing.T) {
	k := key("a")
	doc := locationtest.Sign(t, locationtest.Document(address(k), "hello"), k, location.SignChainID, location.Legacy)
	verifyFails(t, doc, "the record: no gnockpit-location/v1 record in the memo")
}

func TestVerifyRefusesTwoMessagesOrSignatures(t *testing.T) {
	k := key("a")
	doc := unsignedDocument(k)
	sign := func(t *testing.T, doc []byte) []byte {
		return locationtest.Sign(t, doc, k, location.SignChainID, location.Legacy)
	}
	t.Run("two signatures", func(t *testing.T) {
		verifyFails(t, sign(t, sign(t, doc)), "the document holds 2 signatures, want 1")
	})
	t.Run("two messages", func(t *testing.T) {
		tx := decode(t, doc)
		tx.Msgs = append(tx.Msgs, tx.Msgs[0])
		verifyFails(t, sign(t, encode(t, tx)), "the document holds 2 messages, want 1: a send from the operator to itself")
	})
	t.Run("no message", func(t *testing.T) {
		tx := decode(t, doc)
		tx.Msgs = nil
		verifyFails(t, sign(t, encode(t, tx)), "the document holds 0 messages, want 1: a send from the operator to itself")
	})
}

func TestVerifyRefusesNotATransaction(t *testing.T) {
	// amino v1.1.0 panics on a type without a slash; Verify returns the panic's value.
	const noSlash = `not a transaction document: invalid type_url "x", must contain at least one slash and be followed by the full name`
	for _, tc := range []struct{ name, doc, want string }{
		{"text", "hello", ""},
		{"a memo that is a number", `{"memo":1}`, ""},
		{"empty", "", ""},
		{"white space", " \n ", ""},
		{"a message type without a slash", `{"msg":[{"@type":"x"}]}`, noSlash},
		{"a key type without a slash", `{"signatures":[{"pub_key":{"@type":"x"}}]}`, noSlash},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := location.Verify([]byte(tc.doc))
			if err == nil || !strings.HasPrefix(err.Error(), "not a transaction document: ") {
				t.Fatalf("Verify(%q) = %v, want a not a transaction document error", tc.doc, err)
			}
			if tc.want != "" && err.Error() != tc.want {
				t.Fatalf("Verify(%q) = %v, want %q", tc.doc, err, tc.want)
			}
		})
	}
}

// TestVerifyErrorsEchoLittle checks that a refusal repeats little of a
// document amino cannot decode: amino's error quotes its type URL, which may
// fill the document.
func TestVerifyErrorsEchoLittle(t *testing.T) {
	room := location.MaxDocument - len(`{"msg":[{"@type":"x"}]}`)
	for _, tc := range []struct{ name, typeURL string }{
		{"markup", "x" + strings.Repeat("<", room)},
		{"escaped control characters", "x" + strings.Repeat(`\u0001`, room/len(`\u0001`))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := location.Verify([]byte(`{"msg":[{"@type":"` + tc.typeURL + `"}]}`))
			if err == nil || !strings.HasPrefix(err.Error(), "not a transaction document: ") || len(err.Error()) > 200 {
				t.Errorf("Verify(a %d-byte type URL) error = %.300v (%d bytes), want a not a transaction document error under 200 bytes",
					len(tc.typeURL), err, len(fmt.Sprint(err)))
			}
		})
	}
}

func TestVerifyAcceptsSurroundingWhitespace(t *testing.T) {
	doc := []byte("\n  " + string(readFixture(t, "gnokey-v1.7.1.json")) + "\n\n")
	got, err := location.Verify(doc)
	if err != nil {
		t.Fatal(err)
	}
	if got.Operator != gnokeyOperator {
		t.Errorf("Verify(...).Operator = %s, want %s", got.Operator, gnokeyOperator)
	}
	checkSigned(t, got, doc)
}

func TestVerifyAcceptsBothTestRenderings(t *testing.T) {
	k := key("a")
	for _, rc := range renderings {
		t.Run(rc.name, func(t *testing.T) {
			got, err := location.Verify(locationtest.Sign(t, unsignedDocument(k), k, location.SignChainID, rc.r))
			if err != nil {
				t.Fatal(err)
			}
			if want := record(address(k)); got.Record != want {
				t.Errorf("Verify(...).Record = %+v, want %+v", got.Record, want)
			}
		})
	}
}

func TestVerifyReadsARemoval(t *testing.T) {
	k := key("a")
	removal := location.Record{
		Chain: "gnoland1", Operator: address(k), Clear: true, At: time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC),
	}
	doc := locationtest.Sign(t, locationtest.Document(address(k), locationtest.Memo(removal)), k, location.SignChainID, location.Current)
	got, err := location.Verify(doc)
	if err != nil {
		t.Fatal(err)
	}
	if got.Record != removal {
		t.Errorf("Verify(...).Record = %+v, want %+v", got.Record, removal)
	}
}
