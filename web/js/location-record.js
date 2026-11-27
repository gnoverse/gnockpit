// A declared location as the validator page builds it: the record, the
// unsigned document that carries it in its memo, and the commands that
// sign the document with gnokey. The record and the provider match are
// the server's (the location package's ParseMemo, ProviderKey and
// ListedProvider), so what the page shows is what the server stores.

/**
 * A validator's location, as its operator declares it.
 * @typedef {object} LocationRecord
 * @property {string} chain the chain id gnockpit serves
 * @property {string} operator the operator's g1 address
 * @property {number} lat
 * @property {number} lon
 * @property {string} country an ISO 3166-1 alpha-2 code (COUNTRY_CODES)
 * @property {string} provider '' for none, 'on-premise', or a name
 * @property {string} at RFC 3339 in UTC, to the second (recordTime)
 */

/**
 * The removal of an operator's declared location.
 * @typedef {object} RemovalRecord
 * @property {string} chain
 * @property {string} operator
 * @property {true} clear
 * @property {string} at
 */

// The memo's prefix, the record's version.
const MEMO_PREFIX = 'gnockpit-location/v1 '

// ON_PREMISE is the provider of a validator that runs on its operator's
// own hardware (location.OnPremise), always listed.
export const ON_PREMISE = 'on-premise'

// tenth rounds x to the 0.1° grid a record holds, -0 written 0.
/**
 * @param {number} x
 * @returns {number}
 */
export function tenth(x) {
    const rounded = Math.round(x * 10) / 10
    return rounded === 0 ? 0 : rounded
}

// providerKey reduces a provider name to what identifies it: its letters
// and numbers, lowercased. Case, spaces and punctuation do not tell two
// spellings apart. Each code point is lowered alone, as the server lowers
// each letter: a string's toLowerCase writes a capital sigma ending a word
// as ς, which the server keeps apart from σ.
/**
 * @param {string} name
 * @returns {string}
 */
export function providerKey(name) {
    return Array.from(name, (c) => c.toLowerCase())
        .join('')
        .replace(/[^\p{L}\p{N}]/gu, '')
}

// listedProvider is the first of listed, in its order, with name's key,
// so one provider keeps one spelling; a name nothing listed matches as
// written, and '' (no provider) stays ''.
/**
 * @param {string} name
 * @param {readonly string[]} listed
 * @returns {string}
 */
export function listedProvider(name, listed) {
    if (name === '') return ''
    const key = providerKey(name)
    return listed.find((l) => providerKey(l) === key) ?? name
}

// MAX_PROVIDER is the longest provider name, in code points
// (location.MaxProvider).
const MAX_PROVIDER = 64

// providerError is the reason the server refuses provider name, in its
// words, '' for a name it accepts: one longer than MAX_PROVIDER code
// points, one holding a control or a format character, one starting or
// ending with white space, or one holding no letter or digit but '' (no
// provider). The white space trim drops and Go's TrimSpace drops differ
// only in U+FEFF and U+0085, a format and a control character, refused
// first.
/**
 * @param {string} name
 * @returns {string}
 */
export function providerError(name) {
    if (Array.from(name).length > MAX_PROVIDER)
        return 'provider must be at most ' + MAX_PROVIDER + ' characters'
    if (/[\p{Cc}\p{Cf}]/u.test(name))
        return 'provider must hold no control character or format character'
    if (name !== name.trim())
        return 'provider must not start or end with white space'
    if (name !== '' && providerKey(name) === '')
        return 'provider must hold a letter or a digit'
    return ''
}

// recordTime writes a time (ms since the epoch) as a record holds it:
// RFC 3339 in UTC, to the second, ending in Z.
/**
 * @param {number} ms
 * @returns {string}
 */
export function recordTime(ms) {
    return new Date(ms).toISOString().slice(0, 19) + 'Z'
}

// gridCoordinate is x, the coordinate name, on the 0.1° grid; it throws,
// naming it, when the server would refuse it: x is no finite number, or
// lies past -limit or limit once rounded.
/**
 * @param {string} name
 * @param {number} x
 * @param {number} limit
 * @returns {number}
 */
function gridCoordinate(name, x, limit) {
    if (!Number.isFinite(x)) {
        throw new Error(`${name} must be a finite number, not ${x}`)
    }
    const rounded = tenth(x)
    if (rounded < -limit || rounded > limit) {
        throw new Error(
            `${name} must be within -${limit} and ${limit}, not ${x}`
        )
    }
    return rounded
}

// locationMemo is the memo that carries r: the prefix, then r as compact
// JSON, its keys in the order the spec gives and its coordinates on the
// 0.1° grid. It throws on a coordinate the server refuses.
/**
 * @param {LocationRecord | RemovalRecord} r
 * @returns {string}
 */
export function locationMemo(r) {
    const body =
        'clear' in r
            ? { chain: r.chain, operator: r.operator, clear: true, at: r.at }
            : {
                  chain: r.chain,
                  operator: r.operator,
                  lat: gridCoordinate('lat', r.lat, 90),
                  lon: gridCoordinate('lon', r.lon, 180),
                  country: r.country,
                  provider: r.provider,
                  at: r.at,
              }
    return MEMO_PREFIX + JSON.stringify(body)
}

// unsignedDocument is the document gnokey signs for memo: a send of
// 1ugnot from operator to itself, whose only signer is the operator, with
// a fee that passes gnokey's checks and no signature yet.
/**
 * @param {string} memo
 * @param {string} operator
 * @returns {string}
 */
export function unsignedDocument(memo, operator) {
    const doc = {
        msg: [
            {
                '@type': '/bank.MsgSend',
                from_address: operator,
                to_address: operator,
                amount: '1ugnot',
            },
        ],
        fee: { gas_wanted: '1', gas_fee: '1ugnot' },
        signatures: null,
        memo: memo,
    }
    return JSON.stringify(doc, null, 2)
}

// signSnippet is the shell commands that sign document as operator: they
// write it to gnockpit-location.json (a quoted here-document, which
// expands nothing; >| replaces a file left there even under noclobber),
// sign it for gnockpit-location, account 0, sequence 0, and print it only
// once signed, so a failed sign prints no unsigned document to paste
// back. Both cats are the system's cat (command \cat), never an alias
// such as bat, which would decorate what the operator copies back, nor a
// function: command skips functions, and the backslash quotes cat, which
// an alias never matches, even in ksh93, whose command is itself an alias
// ending in a space. It throws on an operator that is no g1 address:
// whatever else would run in the operator's shell.
/**
 * @param {string} document
 * @param {string} operator
 * @returns {string}
 */
export function signSnippet(document, operator) {
    if (!/^g1[0-9a-z]+$/.test(operator)) {
        throw new Error(
            'the operator must be a g1 address, not ' + JSON.stringify(operator)
        )
    }
    return [
        "command \\cat >| gnockpit-location.json <<'EOF'",
        document,
        'EOF',
        `gnokey sign -tx-path gnockpit-location.json -chainid gnockpit-location -account-number 0 -account-sequence 0 ${operator} && command \\cat gnockpit-location.json`,
    ].join('\n')
}
