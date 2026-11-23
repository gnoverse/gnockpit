// Countries as the page shows them: by name and by flag.

// Country name from an ISO 3166-1 alpha-2 code, via the browser's
// Intl.DisplayNames; falls back to the raw code when unavailable.
/** @type {Intl.DisplayNames | null} */
let regionNames = null
try {
    regionNames = new Intl.DisplayNames(['en'], { type: 'region' })
} catch (e) {
    regionNames = null
}
/**
 * @param {string | undefined} code
 * @returns {string}
 */
export function countryName(code) {
    if (!code || code === 'Unknown') return 'Unknown'
    if (regionNames) {
        try {
            const n = regionNames.of(code)
            if (n && n !== code) return n
        } catch (e) {
            // An invalid code shows as it is.
        }
    }
    return code
}
// Flag emoji from an alpha-2 code (regional-indicator code points).
// Empty for Unknown/malformed codes; some platforms (Windows) render
// the two letters instead of a flag.
/**
 * @param {string | undefined} code
 * @returns {string}
 */
export function countryFlag(code) {
    if (!code || code === 'Unknown' || code.length !== 2) return ''
    const base = 0x1f1e6
    const cc = code.toUpperCase()
    return String.fromCodePoint(
        base + (cc.charCodeAt(0) - 65),
        base + (cc.charCodeAt(1) - 65)
    )
}
