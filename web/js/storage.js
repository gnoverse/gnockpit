// The page keeps its settings in localStorage, which a browser
// can refuse (site data blocked) or find full. A refused read
// gives null and a refused write is skipped: the page runs on its
// defaults, and the browser's choice is no error to report.
/**
 * @param {string} key
 * @returns {string | null}
 */
export function storageGet(key) {
    try {
        return localStorage.getItem(key)
    } catch (e) {
        return null
    }
}
/**
 * @param {string} key
 * @param {string} value
 */
export function storageSet(key, value) {
    try {
        localStorage.setItem(key, value)
    } catch (e) {
        // Refused: the setting lasts while the page is open.
    }
}
/** @param {string} key */
export function storageRemove(key) {
    try {
        localStorage.removeItem(key)
    } catch (e) {
        // Refused: the browser keeps nothing for the page.
    }
}
// storageObject reads the JSON object stored at key; anything
// else there, null and broken JSON included, reads as no entries.
/**
 * @param {string} key
 * @returns {Record<string, unknown>}
 */
export function storageObject(key) {
    const stored = storageGet(key)
    if (stored === null) return {}
    try {
        const v = JSON.parse(stored)
        if (v !== null && typeof v === 'object' && !Array.isArray(v)) return v
    } catch (e) {
        // Broken JSON: as if nothing were stored.
    }
    return {}
}
