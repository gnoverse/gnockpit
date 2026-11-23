// How the page names validators, and links to their pages.

// knownValAddrs names validators by address, from the server's
// data. A Map: a peer's self-reported address, such as
// "constructor", is no key of it.
/** @type {Map<string, string>} */
export const knownValAddrs = new Map()
/**
 * @param {string} addr
 * @returns {string | null}
 */
export function valAddrToName(addr) {
    return knownValAddrs.get(addr) || null
}

// shortAddr shortens an address to its first and last characters.
/**
 * @param {string} addr
 * @returns {string}
 */
export function shortAddr(addr) {
    if (!addr || addr.length < 12) return addr || ''
    const prefix = addr.substring(0, 2) // "g1"
    return (
        prefix +
        addr.substring(2, 8) +
        '\u2026' +
        addr.substring(addr.length - 6)
    )
}

// validatorName reads a validator's name as the server sends it:
// the name, and isUs when it carries the " (us)" that marks the
// source's own validator.
/**
 * @param {string | undefined} sent
 * @returns {{ name: string, isUs: boolean }}
 */
export function validatorName(sent) {
    const raw = sent || ''
    return {
        name: raw.replace(' (us)', ''),
        isUs: raw.includes(' (us)'),
    }
}

// shortenAddressName returns how a validator's name shows: the
// name, shortened when it is the validator's address, as the
// server names a validator without a moniker.
/**
 * @param {string} name
 * @param {string} address
 * @returns {string}
 */
export function shortenAddressName(name, address) {
    return name === address ? shortAddr(name) : name
}

// valLink links the page of the validator with address (a plain
// click opens it in a modal: see the validator page section).
// content is its name, as text or a node.
/**
 * @param {string} address
 * @param {string | Node} content
 * @returns {HTMLAnchorElement}
 */
export function valLink(address, content) {
    const a = document.createElement('a')
    a.className = 'val-link'
    a.href = '?val=' + encodeURIComponent(address)
    if (typeof content === 'string') a.textContent = content
    else a.appendChild(content)
    return a
}
